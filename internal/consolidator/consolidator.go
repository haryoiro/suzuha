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
	KeepIndices    []int
	Memories       []memory.Memory
	AffinityDeltas []AffinityDelta
}

// AffinityDelta represents an affinity change detected by the consolidator.
type AffinityDelta struct {
	PlatformUserID string
	Platform       string
	Axis           string  // "closeness" | "trust" | "interest"
	Delta          float64
	Reason         string
	MessageIndices []int // which message indices contributed to this assessment
}

// Client is the interface the agent uses to request compaction.
type Client interface {
	Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error)
}

// Server satisfies Client — compaction can be called in-process.
var _ Client = (*Server)(nil)
