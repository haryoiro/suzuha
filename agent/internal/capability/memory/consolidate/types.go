package consolidate

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
)

// Completer はLLM補完呼び出しを抽象化するインターフェース (consumer-side)。
// acquire.Completer と構造は同一だが、サブ package 間で import しないため
// 両 package に個別に定義する (consumer-side interface の定石)。
type Completer interface {
	CompleteRaw(ctx context.Context, msgs []llm.RawMessage) (*llm.Response, error)
}

// ConsolidateOpts は1回の統合実行を制御するオプション。
type ConsolidateOpts struct {
	SimilarityThreshold float64 `json:"similarity_threshold"`
	MaxGroupSize        int     `json:"max_group_size"`
	MaxGroupsPerLLMCall int     `json:"max_groups_per_llm_call"`
	DryRun              bool    `json:"dry_run"`
}

// ConsolidateResult は統合中に行われた処理の結果を報告する。
type ConsolidateResult struct {
	Groups       int
	TotalDeleted int
	TotalMerged  int
}
