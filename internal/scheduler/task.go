package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/notification"
	"github.com/haryoiro/suzuha/internal/user"
)

// CronTask is a pluggable periodic job, analogous to tool.Tool for agent tools.
// Each implementation handles a specific kind of scheduled work (topics, explore, etc.).
type CronTask interface {
	// Name returns a unique identifier for this task type (e.g. "topics", "explore").
	// This is matched against the "task" field in config.yaml job definitions.
	Name() string

	// Description returns a human-readable description.
	Description() string

	// Setup is called once when the scheduler starts. Use it for migrations,
	// initial data loading, etc. It may be called with a nil CronContext field
	// if the corresponding service is unavailable.
	Setup(ctx context.Context, cc *CronContext) error

	// Execute runs one iteration of the task. cfg contains the job-specific
	// configuration from config.yaml, serialized as JSON.
	Execute(ctx context.Context, cc *CronContext, cfg json.RawMessage) error
}

// CronContext provides shared services and environment to all CronTask implementations.
type CronContext struct {
	// Services
	LLM      *llm.Client
	Memory   memory.Store
	Notifier notification.Notifier // Unified notifier (Send + Reply).
	DB       *sql.DB               // Keep for backward compat; prefer typed stores.
	Logger   *slog.Logger

	// Typed stores (Phase 1 — prefer these over raw DB).
	Users           user.Store             // User operations (resolve, affinity, etc.).
	ChannelActivity channel.ActivityStore  // Channel activity reads.
	MemoryAdmin     memory.AdminStore      // Admin-level memory operations (batch delete, etc.).

	// Event injection
	Bus *event.Bus // Agent event bus for publishing self-prompt events. May be nil.

	// Environment
	Timezone     *time.Location // Scheduler-level timezone. Nil defaults to UTC.
	SystemPrompt string         // Loaded from IDENTITY.md + SOUL.md. Empty if not configured.
}
