package topics

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the self-contained topic posting feature.
// It provides a scheduler task for periodic topic generation but no agent tools.
type Feature struct{}

// New creates a Topics Feature.
func New() *Feature { return &Feature{} }

func (f *Feature) Name() string { return "topics" }

// Setup is a no-op; Topics has no DB tables.
func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

// Tools returns nil; Topics has no agent-side tools.
func (f *Feature) Tools() []tool.Tool { return nil }

// Tasks returns the topic posting scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
