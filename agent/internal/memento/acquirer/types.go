package acquirer

import (
	"github.com/haryoiro/suzuha/internal/memento"
	"github.com/haryoiro/suzuha/internal/memory"
)

// Completer はLLM補完呼び出しを抽象化するインターフェース (consumer-side)。
type Completer = memento.Completer

// AcquireRequest はメモリ抽出のリクエスト。
type AcquireRequest struct {
	Messages []memento.ConversationMessage
}

// AcquireResult は抽出されたメモリを返す結果。
type AcquireResult struct {
	Memories []memory.Memory
}
