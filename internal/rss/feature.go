package rss

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the self-contained RSS monitoring feature.
// It provides agent tools (subscribe, unsubscribe, list, preference)
// and a scheduler task (feed polling + notification).
type Feature struct {
	db  *sql.DB
	mem memory.Store
}

// New creates an RSS Feature.
func New(db *sql.DB, mem memory.Store) *Feature {
	return &Feature{db: db, mem: mem}
}

func (f *Feature) Name() string { return "rss" }

// Setup creates the rss_feeds and rss_items tables.
func (f *Feature) Setup(ctx context.Context, db *sql.DB) error {
	return NewFeedStore(db).Setup(ctx)
}

// Tools returns agent-side tools for RSS management.
func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewSubscribeTool(f.db),
		NewUnsubscribeTool(f.db),
		NewListTool(f.db),
		NewPreferenceTool(f.mem),
	}
}

// Tasks returns the RSS polling scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}}
}

var _ scheduler.Feature = (*Feature)(nil)
