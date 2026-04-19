// Package tool は LLM から呼び出される tool の契約 interface を定義する。
// 実装は各 feature / behavior / capability / channel の `tool_*.go` に配置し、
// tool registry 経由で LLM に公開する。
package tool

import (
	"context"
	"encoding/json"
)

// Tool は built-in / remote 両方に共通する tool の抽象。
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (*ToolResult, error)
}

// ReadOnlyTool はツールが読み取り専用であることを示すオプショナル interface。
// 読み取り専用ツールは他の読み取り専用ツールと並列実行される。
// Tool が ReadOnlyTool を実装しない場合、副作用ありとして直列実行される。
type ReadOnlyTool interface {
	ReadOnly() bool
}

// IsReadOnly はツールが読み取り専用かを返す。
// ReadOnlyTool を実装していなければ false (安全側)。
func IsReadOnly(t Tool) bool {
	if ro, ok := t.(ReadOnlyTool); ok {
		return ro.ReadOnly()
	}
	return false
}

// ToolResult は tool 実行結果を表す。
type ToolResult struct {
	Content   []Content `json:"content"`
	IsError   bool      `json:"isError"`
	StopAfter bool      `json:"-"` // true なら tool loop を抜け、次の LLM 呼び出しを行わない
	ImageURLs []string  `json:"-"` // 任意: tool 結果メッセージに添付する画像 (data URI)
}

// Content は ToolResult の 1 要素。
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextResult は成功したテキスト結果を作る。
func TextResult(text string) *ToolResult {
	return &ToolResult{
		Content: []Content{{Type: "text", Text: text}},
	}
}

// StopResult は tool loop を抜けさせる成功結果を作る。
// reactions のような副作用のみ・以降の LLM 応答不要な tool で使う。
func StopResult(text string) *ToolResult {
	return &ToolResult{
		Content:   []Content{{Type: "text", Text: text}},
		StopAfter: true,
	}
}

// ErrorResult はエラー結果を作る。
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []Content{{Type: "text", Text: msg}},
		IsError: true,
	}
}
