package acquire

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
)

// Completer はLLM補完呼び出しを抽象化するインターフェース (consumer-side)。
type Completer interface {
	CompleteRaw(ctx context.Context, msgs []llm.RawMessage) (*llm.Response, error)
}

// AcquireRequest はメモリ抽出のリクエスト。
type AcquireRequest struct {
	Messages []llm.Message
}

// AcquireResult は抽出されたメモリを返す結果。
type AcquireResult struct {
	Memories []memory.Memory
}
