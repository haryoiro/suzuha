package research

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the fast web research feature.
// Search → parallel fetch top pages. No LLM overhead.
type Feature struct {
	searxngURL string
	maxSources int
}

// New creates a Research Feature.
func New(searxngURL string, maxSources int) *Feature {
	return &Feature{
		searxngURL: searxngURL,
		maxSources: maxSources,
	}
}

func (f *Feature) Name() string { return "research" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

func (f *Feature) Tools() []tool.Tool {
	if f.searxngURL == "" {
		return nil
	}
	return []tool.Tool{
		NewResearchTool(f.searxngURL, f.maxSources),
	}
}

func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
