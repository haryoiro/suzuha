package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config.
	cfgPath := "config.yaml"
	if p := os.Getenv("SUZUHA_CONFIG"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := observe.NewLogger(cfg.Observe.LogLevel)

	// Setup memory store.
	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath, nil, false)
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	defer store.Close()

	// Setup LLM client for consolidation.
	llmClient, err := llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey,
		cfg.LLM.MaxTokens, nil, logger,
	)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	srv := consolidator.NewServer(llmClient, store, logger)

	// TODO: Start gRPC server using srv.Compact as the handler.
	// For now, just log and wait.
	_ = srv

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("suzuha-consolidator started", "address", cfg.Consolidator.Address)
	<-ctx.Done()
	logger.Info("suzuha-consolidator shutting down")
	return nil
}
