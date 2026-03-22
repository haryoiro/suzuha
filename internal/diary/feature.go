package diary

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the diary feature providing hourly digest and daily diary tasks.
type Feature struct{}

func New() *Feature { return &Feature{} }

func (f *Feature) Name() string { return "diary" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool { return nil }

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{
		&HourlyTask{},
		&DailyTask{},
	}
}

var _ scheduler.Feature = (*Feature)(nil)
