package video

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/adapter/transcript"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature は動画理解機能を提供する。
// ツール登録のみで CronTask は持たない。
type Feature struct {
	fetcher   transcript.Fetcher
	extractor transcript.FrameExtractor
	llmClient *llm.Client
	logger    *slog.Logger
}

// New は video Feature を作成する。
// extractor が nil なら video_look は登録されない。
func New(fetcher transcript.Fetcher, extractor transcript.FrameExtractor, llmClient *llm.Client, logger *slog.Logger) *Feature {
	return &Feature{fetcher: fetcher, extractor: extractor, llmClient: llmClient, logger: logger}
}

func (f *Feature) Name() string { return "video" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool {
	tools := []tool.Tool{
		NewWatchTool(f.fetcher, f.logger),
	}
	if f.extractor != nil && f.llmClient != nil {
		tools = append(tools, NewLookTool(f.extractor, f.llmClient, f.logger))
	}
	return tools
}

func (f *Feature) Tasks() []scheduler.CronTask { return nil }

var _ scheduler.Feature = (*Feature)(nil)
