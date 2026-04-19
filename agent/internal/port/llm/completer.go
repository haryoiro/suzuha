package llm

import (
	"context"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// RawMessage は any-llm-go の providers.Message の型エイリアス。
// port 層で LLM 完了呼び出しの入出力型を公開するため外部 SDK 型を再エクスポートする。
type RawMessage = providers.Message

// Response は LLM 完了呼び出しの結果をラップする。
// 複数の consumer (capability/memory の acquire / consolidate 等) が共有するため
// port 層に集約する。
type Response struct {
	Text         string
	Reasoning    string // content inside <think>...</think> tags, if any
	ToolCalls    []providers.ToolCall
	FinishReason string
	Usage        providers.Usage
}

// HasToolCalls は ToolCalls が非空なら true を返す。
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// Completer は LLM 完了呼び出しを抽象化する consumer-side interface。
// capability/memory の acquire / consolidate や将来的な他 consumer が実装を
// 差し替えられるよう port 層で 1 箇所に定義する。
type Completer interface {
	CompleteRaw(ctx context.Context, msgs []RawMessage) (*Response, error)
}
