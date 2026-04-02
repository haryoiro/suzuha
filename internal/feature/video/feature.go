package video

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature は動画理解機能を提供する。
// ツール登録のみで CronTask は持たない。
type Feature struct {
	fetcher transcript.Fetcher
	logger  *slog.Logger
}

// New は video Feature を作成する。
func New(fetcher transcript.Fetcher, logger *slog.Logger) *Feature {
	return &Feature{fetcher: fetcher, logger: logger}
}

func (f *Feature) Name() string { return "video" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool {
	return []tool.Tool{
		NewWatchTool(f.fetcher, f.logger),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask { return nil }

var _ scheduler.Feature = (*Feature)(nil)
