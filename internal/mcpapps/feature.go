package mcpapps

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/mcpbridge"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature implements scheduler.Feature for the MCP Apps system.
type Feature struct {
	mcpMgr   *mcpbridge.Manager
	registry *RegistryClient
	logger   *slog.Logger
	store    *AppStore
}

var _ scheduler.Feature = (*Feature)(nil)

// New creates a new mcpapps Feature.
func New(mcpMgr *mcpbridge.Manager, logger *slog.Logger) *Feature {
	return &Feature{
		mcpMgr:   mcpMgr,
		registry: NewRegistryClient(),
		logger:   logger,
	}
}

func (f *Feature) Name() string { return "mcpapps" }

func (f *Feature) Setup(_ context.Context, db *sql.DB) error {
	f.store = NewAppStore(db)

	ctx := context.Background()
	if err := f.store.Setup(ctx); err != nil {
		return err
	}

	// Auto-reconnect enabled apps from DB.
	apps, err := f.store.ListEnabled(ctx)
	if err != nil {
		f.logger.Warn("mcpapps: failed to list enabled apps", "error", err)
		return nil
	}

	for _, app := range apps {
		srv := app.ToToolServer()
		connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		toolNames, err := f.mcpMgr.ConnectServer(connectCtx, srv)
		cancel()
		if err != nil {
			f.logger.Warn("mcpapps: reconnect failed", "app", app.Name, "error", err)
			continue
		}
		f.logger.Info("mcpapps: reconnected", "app", app.Name, "tools", len(toolNames))
	}

	return nil
}

func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewSearchTool(f.registry),
		NewInstallTool(f.store, f.mcpMgr, f.registry, f.logger),
		NewUninstallTool(f.store, f.mcpMgr),
		NewListAppsTool(f.store, f.mcpMgr),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return nil
}
