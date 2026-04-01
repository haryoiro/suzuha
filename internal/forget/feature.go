package forget

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/memento"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// consolidator は forget が必要とする統合機能を定義する (consumer-side interface)。
type consolidator interface {
	Consolidate(ctx context.Context, opts *memento.ConsolidateOpts) (*memento.ConsolidateResult, error)
}

// Feature は定期的なメモリ重複排除用のスケジューラタスクを提供する。
// 全てのロジックを consolidator インターフェースに委譲する。
type Feature struct {
	consolidator consolidator
}

func New(c consolidator) *Feature {
	return &Feature{consolidator: c}
}

func (f *Feature) Name() string { return "forget" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool { return nil }

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{consolidator: f.consolidator}}
}

var _ scheduler.Feature = (*Feature)(nil)
