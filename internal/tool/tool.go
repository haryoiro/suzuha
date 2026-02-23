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

// ToolResult is the result of a tool execution.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
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

// ErrorResult creates an error result.
func ErrorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []Content{{Type: "text", Text: msg}},
		IsError: true,
	}
}
