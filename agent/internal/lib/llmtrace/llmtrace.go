// Package llmtrace は LLM メッセージ / レスポンスを Langfuse などの
// トレースシンクに流すための JSON 文字列化ヘルパを提供する。
package llmtrace

import (
	"encoding/json"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/llmconv"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// SerializeMessages は messages を Langfuse input 用の JSON 配列にする。
// 画像は payload を肥大化させるため除外する。user メッセージには
// llmconv.ConvertMessages と同じメタデータ prefix を付与する。
func SerializeMessages(messages []message.Message) string {
	type traceMsg struct {
		Role     string `json:"role"`
		Content  string `json:"content"`
		Name     string `json:"name,omitempty"`
		Injected bool   `json:"injected,omitempty"`
	}
	out := make([]traceMsg, 0, len(messages))
	for _, m := range messages {
		name := m.UserName
		if m.Role == "tool" {
			name = m.ToolCallID
		}
		content := m.Content
		if prefix := llmconv.UserMessagePrefix(m); prefix != "" {
			content = prefix + m.Content
		}
		out = append(out, traceMsg{
			Role:     m.Role,
			Content:  content,
			Name:     name,
			Injected: m.Injected,
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// SerializeResponse は Response を Langfuse output 用の JSON にする。
func SerializeResponse(r *portllm.Response) string {
	type traceToolCall struct {
		Name string `json:"name"`
		Args string `json:"arguments"`
	}
	type traceResp struct {
		Text      string          `json:"text"`
		ToolCalls []traceToolCall `json:"tool_calls,omitempty"`
	}
	resp := traceResp{Text: r.Text}
	for _, tc := range r.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, traceToolCall{
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}
	b, _ := json.Marshal(resp)
	return string(b)
}
