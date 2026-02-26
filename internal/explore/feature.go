package explore

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the self-contained web exploration feature.
// It provides a scheduler task for autonomous internet exploration but no agent tools.
type Feature struct{}

// New creates an Explore Feature.
func New() *Feature { return &Feature{} }

func (f *Feature) Name() string { return "explore" }

// Setup is a no-op; Explore uses the shared task_state table.
func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

// Tools returns nil; Explore has no agent-side tools.
func (f *Feature) Tools() []tool.Tool { return nil }

// Tasks returns the exploration scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
