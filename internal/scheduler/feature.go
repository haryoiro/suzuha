package scheduler

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature bundles related scheduler tasks, agent tools, and DB setup into
// a single self-contained package. Each feature (RSS, Topics, etc.) implements
// this interface. main.go registers features in a loop.
type Feature interface {
	// Name returns the feature identifier (e.g. "rss", "topics").
	Name() string

	// Setup performs one-time initialization such as creating DB tables.
	// Must be idempotent (safe to call multiple times).
	Setup(ctx context.Context, db *sql.DB) error

	// Tools returns agent-side tools provided by this feature.
	// Return nil if the feature has no tools.
	Tools() []tool.Tool

	// Tasks returns scheduler tasks provided by this feature.
	// Return nil if the feature has no tasks.
	Tasks() []CronTask
}
