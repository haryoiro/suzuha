package affinity

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature provides the lightweight affinity evaluation scheduler task.
type Feature struct{}

func New() *Feature { return &Feature{} }

func (f *Feature) Name() string { return "affinity" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool { return nil }

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
