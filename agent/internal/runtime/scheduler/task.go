package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	portconv "github.com/haryoiro/suzuha/internal/port/conversation"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	"github.com/haryoiro/suzuha/internal/port/user"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler/notification"
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

// CronContext は全 CronTask 実装に共有サービスと環境を渡すコンテナ。
type CronContext struct {
	// Services
	LLM      portllm.Client
	Memory   portmem.Memory
	Notifier notification.Notifier
	DB       *sql.DB // Keep for backward compat; prefer typed stores.
	Logger   *slog.Logger

	// Typed stores
	Users           user.Store
	ChannelActivity portconv.ActivityStore
	MemoryAdmin     portmem.Management
	MediaStore      portmem.Media

	// Event injection
	Bus *event.Bus

	// Environment
	Timezone     *time.Location
	SystemPrompt string
}
