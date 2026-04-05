package bench

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

// RunnerConfig はベンチマークランナーの設定。
type RunnerConfig struct {
	SnapshotPath string // pg_dump ファイルパス
	BenchDBURL   string // ベンチ用 DB の DSN
}

// Runner はベンチマークを実行する。
// Agent は外部から注入される (本番と同じ DI パスで構築されたもの)。
type Runner struct {
	cfg    RunnerConfig
	ag     *agent.Agent
	logger *slog.Logger

	mu            sync.Mutex
	currentCaseID string
	responses     map[string]string
}

// NewRunner はスナップショットを復元し、ランナーを作成する。
// Agent は呼び出し側が本番と同じ DI で構築して渡す。
func NewRunner(cfg RunnerConfig, ag *agent.Agent, logger *slog.Logger) (*Runner, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// ベンチ用 DB にスナップショットを復元
	if cfg.SnapshotPath != "" {
		if err := restoreSnapshot(cfg.SnapshotPath, cfg.BenchDBURL); err != nil {
			return nil, fmt.Errorf("bench: snapshot 復元に失敗: %w", err)
		}
	}

	r := &Runner{
		cfg:       cfg,
		ag:        ag,
		logger:    logger,
		responses: make(map[string]string),
	}

	// Session を応答キャプチャ用に差し替え
	ag.SetSession(agent.SourceKeyDiscord, &captureSession{
		agentCtx: ag.AgentContextFor(agent.SourceKeyDiscord),
		runner:   r,
	})

	return r, nil
}

// RunScenario はシナリオの全テストケースを実行する。
// 各ケースはシナリオの inject_logs をベースに独立したコンテキストで実行される。
func (r *Runner) RunScenario(ctx context.Context, s *Scenario) []Result {
	var results []Result

	for _, tc := range s.Cases {
		// ケースごとにコンテキストをリセットして inject_logs から再構築
		agentCtx := r.ag.AgentContextFor(agent.SourceKeyDiscord)
		agentCtx.ReplaceAll(nil)

		r.injectLogs(s.InjectLogs)

		result := r.runCase(ctx, s.Name, tc)
		results = append(results, result)
		r.logger.Info("ケース完了",
			"scenario", s.Name,
			"case", tc.ID,
			"response_len", len(result.Response),
		)
	}

	return results
}

func (r *Runner) injectLogs(logs []InjectLog) {
	agentCtx := r.ag.AgentContextFor(agent.SourceKeyDiscord)
	for _, l := range logs {
		channel := l.Channel
		if channel == "" {
			channel = "bench-channel"
		}
		msg := llm.Message{
			Role:      l.Role,
			UserName:  l.UserName,
			Content:   l.Content,
			Channel:   channel,
			Timestamp: jtime.Now(),
		}
		agentCtx.Add(msg)
	}
}

func (r *Runner) runCase(ctx context.Context, scenarioName string, tc TestCase) Result {
	result := Result{
		ScenarioName: scenarioName,
		CaseID:       tc.ID,
		Prompt:       tc.Prompt,
		Expect:       tc.Expect,
	}

	// マルチターンの場合
	if len(tc.Turns) > 0 {
		return r.runMultiTurn(ctx, scenarioName, tc)
	}

	r.sendAndCapture(ctx, tc.ID, tc.Prompt, tc.Source, tc.Channel)

	r.mu.Lock()
	result.Response = r.responses[tc.ID]
	r.mu.Unlock()

	return result
}

func (r *Runner) runMultiTurn(ctx context.Context, scenarioName string, tc TestCase) Result {
	result := Result{
		ScenarioName: scenarioName,
		CaseID:       tc.ID,
		Expect:       tc.Expect,
	}

	for i, turn := range tc.Turns {
		prompt := turn.Prompt
		if prompt == "" {
			// claude -p で次のプロンプトを生成
			prompt = r.generateNextPrompt(tc, result.TurnResults, turn.Goal)
		}

		turnID := fmt.Sprintf("%s_turn%d", tc.ID, i)
		r.sendAndCapture(ctx, turnID, prompt, tc.Source, tc.Channel)

		r.mu.Lock()
		resp := r.responses[turnID]
		r.mu.Unlock()

		result.TurnResults = append(result.TurnResults, TurnResult{
			TurnIndex: i,
			Prompt:    prompt,
			Response:  resp,
			Goal:      turn.Goal,
		})
	}

	// 最後のターンの応答を result.Response にも入れる
	if len(result.TurnResults) > 0 {
		last := result.TurnResults[len(result.TurnResults)-1]
		result.Prompt = last.Prompt
		result.Response = last.Response
	}

	return result
}

func (r *Runner) sendAndCapture(ctx context.Context, caseID, prompt, source, channel string) {
	if source == "" {
		source = "discord"
	}
	if channel == "" {
		channel = "bench-channel"
	}

	var evt event.Event
	if source == "internal" {
		evt = event.NewSelfPromptEvent(channel, prompt)
	} else {
		evt = event.NewMessageEvent(source, event.MessagePayload{
			Content:   prompt,
			Channel:   channel,
			UserID:    "bench-user",
			UserName:  "テストユーザー",
			IsMention: true,
		})
	}

	r.mu.Lock()
	r.currentCaseID = caseID
	r.mu.Unlock()

	r.ag.HandleBatch(ctx, []event.Event{evt})
}

// generateNextPrompt は claude -p で会話の流れから次のプロンプトを生成する。
func (r *Runner) generateNextPrompt(tc TestCase, history []TurnResult, goal string) string {
	var sb strings.Builder
	sb.WriteString("以下の会話の続きとして、次のユーザー発言を1文で生成してください。発言のみ返してください。\n\n")
	sb.WriteString("目標: " + goal + "\n\n")
	sb.WriteString("会話履歴:\n")
	for _, tr := range history {
		fmt.Fprintf(&sb, "ユーザー: %s\n", tr.Prompt)
		fmt.Fprintf(&sb, "AI: %s\n", tr.Response)
	}

	cmd := exec.Command("claude", "-p", "--output-format", "text")
	cmd.Stdin = strings.NewReader(sb.String())
	out, err := cmd.Output()
	if err != nil {
		r.logger.Warn("claude -p でプロンプト生成に失敗", "error", err)
		return goal // フォールバック: goal をそのままプロンプトに
	}
	return strings.TrimSpace(string(out))
}

// restoreSnapshot は pg_restore でスナップショットをベンチ用 DB に復元する。
func restoreSnapshot(snapshotPath, dbURL string) error {
	dropCmd := exec.Command("psql", dbURL, "-c",
		`DO $$ DECLARE r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
				EXECUTE 'DROP TABLE IF EXISTS ' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$;`)
	if out, err := dropCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("テーブル削除に失敗: %s: %w", string(out), err)
	}

	restoreCmd := exec.Command("pg_restore",
		"--dbname", dbURL,
		"--no-owner",
		"--no-privileges",
		"--clean",
		"--if-exists",
		snapshotPath,
	)
	if out, err := restoreCmd.CombinedOutput(); err != nil {
		slog.Warn("pg_restore 警告", "output", string(out))
	}

	return nil
}

// captureSession は応答テキストをキャプチャする Session 実装。
// 本番の Session と同じ DirectiveConfig を持ちつつ、Respond でテキストをキャプチャする。
type captureSession struct {
	agentCtx *agent.Context
	runner   *Runner
}

var _ agent.Session = (*captureSession)(nil)

func (s *captureSession) Source() agent.SourceKey       { return agent.SourceKeyDiscord }
func (s *captureSession) Context() *agent.Context       { return s.agentCtx }
func (s *captureSession) PersistKey() string            { return "bench_discord" }
func (s *captureSession) BeginTurn(p *agent.Perception) {}
func (s *captureSession) DirectiveConfig() agent.DirectiveConfig {
	return agent.DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        0,
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: false, // 会話履歴の注入は有効にする
	}
}

func (s *captureSession) Respond(_ context.Context, text string) error {
	s.runner.mu.Lock()
	defer s.runner.mu.Unlock()
	s.runner.responses[s.runner.currentCaseID] = text
	return nil
}
