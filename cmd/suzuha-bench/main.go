// suzuha-bench は会話品質のベンチマークを実行する。
//
// 使い方:
//
//	go run ./cmd/suzuha-bench \
//	  -config config.yaml \
//	  -scenarios bench/scenarios/ \
//	  -snapshot bench/snapshots/baseline.dump \
//	  -bench-db "postgres://suzuha:suzuha@suzuha-db:5432/suzuha_bench?sslmode=disable" \
//	  -identity .suzuha/IDENTITY.md \
//	  -output bench/results.json
//
// 本番と同じ DI パスで Agent を構築し、DB のみベンチ用に差し替える。
// 応答を claude -p で評価する。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/haryoiro/suzuha/internal/bench"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/conversation"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config.yaml のパス")
	scenarioDir := flag.String("scenarios", "bench/scenarios", "シナリオディレクトリ")
	snapshotPath := flag.String("snapshot", "", "pg_dump スナップショットパス (省略時はスナップショット復元なし)")
	benchDB := flag.String("bench-db", "", "ベンチ用 DB の DSN (省略時は config の postgres_url を使用)")
	identityPath := flag.String("identity", ".suzuha/IDENTITY.md", "IDENTITY.md のパス")
	outputPath := flag.String("output", "", "結果 JSON の出力パス (省略時は標準出力のみ)")
	skipEval := flag.Bool("skip-eval", false, "評価をスキップ (応答生成のみ)")
	flag.Parse()

	if err := run(*cfgPath, *scenarioDir, *snapshotPath, *benchDB, *identityPath, *outputPath, *skipEval); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(cfgPath, scenarioDir, snapshotPath, benchDBURL, identityPath, outputPath string, skipEval bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config 読み込みに失敗: %w", err)
	}

	// ベンチ用 DB DSN (指定がなければ config から)
	dbURL := benchDBURL
	if dbURL == "" {
		dbURL = cfg.Memory.PostgresURL
	}
	if dbURL == "" {
		return fmt.Errorf("-bench-db または config.memory.postgres_url を指定してください")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// シナリオ読み込み
	scenarios, err := bench.LoadAllScenarios(scenarioDir)
	if err != nil {
		return err
	}
	logger.Info("シナリオ読み込み完了", "count", len(scenarios))

	// スナップショット復元
	if snapshotPath != "" {
		logger.Info("スナップショット復元中", "path", snapshotPath)
		// Runner 内で復元するので、ここでは cfg に設定するだけ
	}

	// Agent を本番と同等の構成で構築 (DB だけベンチ用)
	ag, store, err := buildAgent(cfg, dbURL, snapshotPath, logger)
	if err != nil {
		return fmt.Errorf("Agent 構築に失敗: %w", err)
	}
	defer store.Close()

	// ベンチ実行
	ctx := context.Background()
	var allResults []bench.Result

	for _, s := range scenarios {
		logger.Info("シナリオ実行中", "name", s.Name, "cases", len(s.Cases))

		runner, err := bench.NewRunner(bench.RunnerConfig{
			SnapshotPath: snapshotPath,
			BenchDBURL:   dbURL,
		}, ag, logger)
		if err != nil {
			return fmt.Errorf("Runner 構築に失敗: %w", err)
		}

		results := runner.RunScenario(ctx, s)
		allResults = append(allResults, results...)
	}

	// 評価
	if !skipEval {
		logger.Info("claude -p で評価中", "count", len(allResults))
		allResults, err = bench.Evaluate(allResults, identityPath)
		if err != nil {
			return fmt.Errorf("評価に失敗: %w", err)
		}
	}

	// レポート出力
	bench.PrintReport(allResults)

	// JSON 出力
	if outputPath != "" {
		data, _ := json.MarshalIndent(allResults, "", "  ")
		if err := os.WriteFile(outputPath, data, 0644); err != nil {
			return fmt.Errorf("結果の書き込みに失敗: %w", err)
		}
		logger.Info("結果を保存", "path", outputPath)
	}

	return nil
}

// buildAgent は本番と同等の DI で Agent を構築する (DB のみベンチ用)。
func buildAgent(cfg *config.Config, dbURL, snapshotPath string, logger *slog.Logger) (*agent.Agent, memory.Backend, error) {
	// Memory Store (ParadeDB)
	store, err := memory.NewPostgresStore(dbURL, nil, true, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("PostgresStore 構築に失敗: %w", err)
	}

	db := store.DB()
	convStore := conversation.NewStore(db)
	userStore := user.NewSQLiteStore(db)

	// LLM Client (本番と同じ設定)
	embCfg := llm.EmbeddingConfig{
		Provider: cfg.Embedding.Provider,
		Model:    cfg.Embedding.Model,
		APIKey:   cfg.Embedding.APIKey,
		APIBase:  cfg.Embedding.APIBase,
	}
	visCfg := llm.VisionConfig{
		Provider: cfg.Vision.Provider,
		Model:    cfg.Vision.Model,
		APIKey:   cfg.Vision.APIKey,
		APIBase:  cfg.Vision.APIBase,
	}
	llmClient, err := llm.NewClient(cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase, cfg.LLM.MaxTokens, embCfg, visCfg, logger)
	if err != nil {
		store.Close()
		return nil, nil, fmt.Errorf("LLM Client 構築に失敗: %w", err)
	}

	bus := event.NewBus(64)

	regs := []agent.SourceRegistration{
		{
			Key: agent.SourceKeyDiscord,
			NewSession: func(agentCtx *agent.Context) agent.Session {
				// captureSession に後で差し替えるのでダミー
				return agent.NewDiscordSession(agentCtx, nil, nil, nil, 0, logger)
			},
			PersistKey: "bench_discord",
		},
	}

	ag := agent.New(
		agent.Config{
			SystemPrompt:     cfg.Agent.SystemPrompt,
			BotID:            "bench-bot",
			ContextWindowPct: 0.8,
			MaxContextTokens: cfg.LLM.MaxTokens,
			DrainWindow:      -1,
		},
		regs,
		llmClient,
		tool.NewRegistry(),
		store,
		userStore,
		bus,
		nil, // acquirer
		convStore,
		db,
		nil, // channelSettings
		logger,
	)

	return ag, store, nil
}
