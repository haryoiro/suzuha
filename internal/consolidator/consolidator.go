package consolidator

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// CompactRequest is sent from the agent to request memory extraction
// before context is cleared.
type CompactRequest struct {
	Messages []llm.Message
}

// CompactResult is returned by the consolidator with extracted memories.
type CompactResult struct {
	Memories []memory.Memory
}

// Client is the interface the agent uses to request compaction.
type Client interface {
	Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error)
}

// Server satisfies Client — compaction can be called in-process.
var _ Client = (*Server)(nil)
