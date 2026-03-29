package research

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature is the fast deep-research exploration feature.
// Parallel searches + parallel fetches + 2 LLM calls.
type Feature struct {
	searxngURL   string
	llm          *llm.Client
	mem          memory.Store
	systemPrompt string
	breadth      int
	maxSources   int
}

// New creates an Explore Feature.
func New(searxngURL string, llmClient *llm.Client, memStore memory.Store, systemPrompt string, breadth, maxSources int) *Feature {
	return &Feature{
		searxngURL:   searxngURL,
		llm:          llmClient,
		mem:          memStore,
		systemPrompt: systemPrompt,
		breadth:      breadth,
		maxSources:   maxSources,
	}
}

func (f *Feature) Name() string { return "research" }

func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

// Tools returns the explore tool for the agent.
func (f *Feature) Tools() []tool.Tool {
	if f.searxngURL == "" {
		return nil
	}
	return []tool.Tool{
		NewExploreTool(f.searxngURL, f.llm, f.mem, f.systemPrompt, f.breadth, f.maxSources),
	}
}

// Tasks returns the exploration scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
