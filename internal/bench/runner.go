package bench

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
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
func (r *Runner) RunScenario(ctx context.Context, s *Scenario) []Result {
	var results []Result

	// 会話ログをコンテキストに注入
	r.injectLogs(s.InjectLogs)

	for _, tc := range s.Cases {
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

	source := "discord"
	if tc.Source != "" {
		source = tc.Source
	}

	channel := tc.Channel
	if channel == "" {
		channel = "bench-channel"
	}

	evt := event.NewMessageEvent(source, event.MessagePayload{
		Content:   tc.Prompt,
		Channel:   channel,
		UserID:    "bench-user",
		UserName:  "テストユーザー",
		IsMention: source != "internal",
	})

	r.mu.Lock()
	r.currentCaseID = tc.ID
	r.mu.Unlock()

	err := r.ag.HandleBatch(ctx, []event.Event{evt})
	if err != nil {
		result.Error = err.Error()
		return result
	}

	r.mu.Lock()
	result.Response = r.responses[tc.ID]
	r.mu.Unlock()

	return result
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
