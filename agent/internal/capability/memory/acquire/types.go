package acquire

import (
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/llm"
)

// AcquireRequest はメモリ抽出のリクエスト。
type AcquireRequest struct {
	Messages []llm.Message
}

// AcquireResult は抽出されたメモリを返す結果。
type AcquireResult struct {
	Memories []memory.Memory
}
