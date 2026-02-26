package schedule

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature bundles schedule tools and tasks.
type Feature struct {
	db *sql.DB
}

var _ scheduler.Feature = (*Feature)(nil)

// New creates a new schedule Feature.
func New(db *sql.DB) *Feature {
	return &Feature{db: db}
}

func (f *Feature) Name() string { return "schedule" }

func (f *Feature) Setup(ctx context.Context, db *sql.DB) error {
	return NewStore(db).Setup(ctx)
}

func (f *Feature) Tools() []tool.Tool {
	store := NewStore(f.db)
	return []tool.Tool{
		NewCreateTool(store),
		NewListTool(store),
		NewCancelTool(store),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}
