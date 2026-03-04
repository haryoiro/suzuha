package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/samber/do/v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := "config.yaml"
	if p := os.Getenv("SUZUHA_CONFIG"); p != "" {
		cfgPath = p
	}

	injector := do.New(adminPackages(cfgPath)...)

	logger := do.MustInvoke[*slog.Logger](injector)
	store := do.MustInvoke[*memory.SQLiteStore](injector)
	defer store.Close()

	srv := do.MustInvoke[*admin.Server](injector)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			logger.Error("admin server failed", "error", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("suzuha-admin shutting down")
	return nil
}
