package dyntools

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature implements scheduler.Feature for the dynamic tool system.
type Feature struct {
	manager *Manager
}

var _ scheduler.Feature = (*Feature)(nil)

// New creates a new dyntools Feature.
func New(toolsDir string, registry *tool.Registry, logger *slog.Logger) *Feature {
	mgr := NewManager(toolsDir, registry, logger)
	return &Feature{manager: mgr}
}

func (f *Feature) Name() string { return "dyntools" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error {
	return f.manager.LoadAll()
}

func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewCreateTool(f.manager),
		NewDeleteTool(f.manager),
		NewListTool(f.manager),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return nil
}
