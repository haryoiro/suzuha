package mcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/config"
	toolreg "github.com/haryoiro/suzuha/internal/runtime/toolregistry"
	"github.com/samber/do/v2"
)

// Package registers MCP bridge providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Manager, error) {
		logger := do.MustInvoke[*slog.Logger](i)
		registry := do.MustInvoke[*toolreg.Registry](i)
		cfg := do.MustInvoke[*config.Config](i)
		mgr := NewManager(logger, registry)
		startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		mgr.Start(startCtx, cfg.ToolServers)
		cancel()
		return mgr, nil
	})
}
