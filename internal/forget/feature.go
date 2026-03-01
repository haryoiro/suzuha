package forget

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature provides a scheduler task for periodic memory deduplication.
type Feature struct{}

func New() *Feature { return &Feature{} }

func (f *Feature) Name() string { return "forget" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool { return nil }

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
