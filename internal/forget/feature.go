package forget

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature は定期的なメモリ重複排除用のスケジューラタスクを提供する。
// 全てのロジックを consolidator.Maintainer インターフェースに委譲する。
type Feature struct {
	maintainer consolidator.Maintainer
}

func New(m consolidator.Maintainer) *Feature {
	return &Feature{maintainer: m}
}

func (f *Feature) Name() string { return "forget" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool { return nil }

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{maintainer: f.maintainer}}
}

var _ scheduler.Feature = (*Feature)(nil)
