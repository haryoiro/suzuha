package research

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the web research feature.
// Search → LLM picks relevant results → parallel fetch.
type Feature struct {
	searxngURL string
	llm        *llm.Client
	maxSources int
}

// New creates a Research Feature.
func New(searxngURL string, llmClient *llm.Client, maxSources int) *Feature {
	return &Feature{
		searxngURL: searxngURL,
		llm:        llmClient,
		maxSources: maxSources,
	}
}

func (f *Feature) Name() string { return "research" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

// Tools returns the research tool for the agent.
func (f *Feature) Tools() []tool.Tool {
	if f.searxngURL == "" {
		return nil
	}
	return []tool.Tool{
		NewResearchTool(f.searxngURL, f.llm, f.maxSources),
	}
}

// Tasks returns the research scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
