package memento

import (
	"context"
	"strings"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// completer はLLM補完呼び出しを抽象化するインターフェース。
// *llm.RoleClient が実装する。テストではモックに差し替え可能。
type completer interface {
	CompleteRaw(ctx context.Context, msgs []llm.RawMessage) (*llm.Response, error)
}

// --- Acquire 系 ---

// AcquireRequest はコンテキストがクリアされる前にメモリ抽出を要求するリクエスト。
type AcquireRequest struct {
	Messages []llm.Message
}

// AcquireResult は抽出されたメモリを返す結果。
type AcquireResult struct {
	Memories []memory.Memory
}

// --- Consolidate 系 ---

// ConsolidateOpts は1回の統合実行を制御するオプション。
// json タグ付きで、cron設定から直接 unmarshal 可能。
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

// --- 共有ユーティリティ ---

// stripJSONFence はJSON周囲のMarkdownコードフェンスを除去する。
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
