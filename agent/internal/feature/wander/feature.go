package wander

import (
	"context"
	"database/sql"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// memorySaver はメモリの保存機能を提供する (consumer-side interface)。
type memorySaver interface {
	Save(ctx context.Context, mem *memory.Memory) error
}

// Feature is the self-contained web wandering feature.
// It provides a scheduler task for autonomous wandering and an agent tool.
type Feature struct {
	searxngURL   string
	llm          *llm.Client
	mem          memorySaver
	systemPrompt string
	maxDepth     int
}

// New creates a Wander Feature.
func New(searxngURL string, llmClient *llm.Client, memStore memorySaver, systemPrompt string, maxDepth int) *Feature {
	return &Feature{
		searxngURL:   searxngURL,
		llm:          llmClient,
		mem:          memStore,
		systemPrompt: systemPrompt,
		maxDepth:     maxDepth,
	}
}

func (f *Feature) Name() string { return "wander" }

// Setup is a no-op; Wander uses the shared task_state table.
func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }

// Tools returns the wander tool for the agent.
func (f *Feature) Tools() []tool.Tool {
	if f.searxngURL == "" {
		return nil
	}
	return []tool.Tool{
		NewWanderTool(f.searxngURL, f.llm, f.mem, f.systemPrompt, f.maxDepth),
	}
}

// Tasks returns the wandering scheduler task.
func (f *Feature) Tasks() []scheduler.CronTask {
	return []scheduler.CronTask{&Task{}}
}

var _ scheduler.Feature = (*Feature)(nil)
