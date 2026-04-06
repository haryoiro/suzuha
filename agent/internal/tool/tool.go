package tool

import (
	"context"
	"encoding/json"
)

// Tool is the common abstraction for built-in and remote tools.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (*ToolResult, error)
}

// ReadOnlyTool はツールが読み取り専用であることを示すオプショナルインターフェース。
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

// ToolResult is the result of a tool execution.
type ToolResult struct {
	Content   []Content `json:"content"`
	IsError   bool      `json:"isError"`
	StopAfter bool      `json:"-"` // If true, stop the tool loop without making another LLM call.
	ImageURLs []string  `json:"-"` // Optional: images to attach to the tool result message (data URIs).
}

// Content is a single piece of content in a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextResult creates a successful text result.
func TextResult(text string) *ToolResult {
	return &ToolResult{
		Content: []Content{{Type: "text", Text: text}},
	}
}

// StopResult creates a successful result that stops the tool loop.
// Use this for side-effect-only tools (e.g. reactions) where no further
// LLM response is needed after execution.
func StopResult(text string) *ToolResult {
	return &ToolResult{
		Content:   []Content{{Type: "text", Text: text}},
		StopAfter: true,
	}
}

// ErrorResult creates an error result.
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []Content{{Type: "text", Text: msg}},
		IsError: true,
	}
}
