package memory

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	"github.com/haryoiro/suzuha/internal/llm"
)

// AcquireRequest は会話から記憶を抽出するためのリクエスト。
type AcquireRequest struct {
	Messages []llm.Message
}

// AcquireResult は抽出された記憶を返す結果。
type AcquireResult struct {
	Memories []memo.Memory
}

// Acquirer は会話からの記憶抽出を抽象化する契約。
// capability/memory/acquire.Acquirer が実装する。
type Acquirer interface {
	Acquire(ctx context.Context, req *AcquireRequest) (*AcquireResult, error)
}
