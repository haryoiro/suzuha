package consolidator

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// CompactRequest is sent from the agent to request context compaction.
type CompactRequest struct {
	Messages    []llm.Message
	TargetCount int
}

// CompactResult is returned by the consolidator with compaction decisions.
type CompactResult struct {
	KeepIndices []int
	Memories    []memory.Memory
}

// Client is the interface the agent uses to request compaction.
type Client interface {
	Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error)
}
