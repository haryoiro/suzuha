package consolidator

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// CompactRequest はコンテキストがクリアされる前にメモリ抽出を要求するために
// エージェントから送信されるリクエスト。
type CompactRequest struct {
	Messages []llm.Message
}

// CompactResult はコンソリデーターが抽出したメモリを返す結果。
type CompactResult struct {
	Memories []memory.Memory
}

// Client はエージェントがコンパクション（メモリ圧縮）を要求するためのインターフェース。
type Client interface {
	Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error)
}

// MaintainOpts は1回のメンテナンス実行を制御するオプション。
// json タグ付きで、cron設定から直接 unmarshal 可能。
type MaintainOpts struct {
	SimilarityThreshold float64 `json:"similarity_threshold"`
	MaxGroupSize        int     `json:"max_group_size"`
	MaxGroupsPerLLMCall int     `json:"max_groups_per_llm_call"`
	DryRun              bool    `json:"dry_run"`
}

// MaintainResult はメンテナンス中に行われた処理の結果を報告する。
type MaintainResult struct {
	Groups       int
	TotalDeleted int
	TotalMerged  int
}

// Maintainer は定期的なメモリの重複排除と統合を実行するインターフェース。
type Maintainer interface {
	Maintain(ctx context.Context, opts MaintainOpts) (*MaintainResult, error)
}

// Server は Client と Maintainer の両方を満たす。
var _ Client = (*Server)(nil)
var _ Maintainer = (*Server)(nil)
