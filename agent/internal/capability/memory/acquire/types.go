package acquire

import (
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/llm"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// Completer は port/llm.Completer の型エイリアス。
// acquire 側で bare 名を使うための短縮。正準定義は port/llm/。
type Completer = portllm.Completer

// AcquireRequest はメモリ抽出のリクエスト。
type AcquireRequest struct {
	Messages []llm.Message
}

// AcquireResult は抽出されたメモリを返す結果。
type AcquireResult struct {
	Memories []memory.Memory
}
