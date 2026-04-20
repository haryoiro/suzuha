// Package llmconv は suzuha の内部メッセージ / ツール表現を any-llm-go
// (providers パッケージ) の形式に変換する純粋関数を提供する。
//
// architecture.md の lib 層規約では「domain に依存するコード / 外部 SDK 型」は
// lib に置いてはならないが、本 package は **暫定的な例外** として存置する:
//
//   - runtime/agent と capability/llm の両方が domain→SDK 変換を必要とする
//   - 理想的には port/llm.Complete が domain 型を受け取り、adapter 内で SDK 変換する
//   - この refactor は port/llm interface 変更を伴うため、別 Phase で対応予定
//
// 暫定的に lib 配置 + 副作用なし (slog warn のみ) で維持する。
package llmconv

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/port/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// ConvertMessages は suzuha message.Message を any-llm-go providers.Message に変換する。
// 2 個目以降の system メッセージは user に変換される (Qwen3.5 等の制約)。
// 対応する assistant tool_calls のない孤児 tool メッセージは除外し、
// OpenAI の strict モードを満たす形に正規化する。
func ConvertMessages(msgs []message.Message, visionCapable bool) []providers.Message {
	assistantToolCalls := make(map[string]bool)
	toolResponses := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				assistantToolCalls[tc.ID] = true
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			toolResponses[m.ToolCallID] = true
		}
	}

	out := make([]providers.Message, 0, len(msgs))
	seenSystem := false

	for _, m := range msgs {
		role := m.Role
		content := m.Content

		if role == "tool" && m.ToolCallID != "" && !assistantToolCalls[m.ToolCallID] {
			continue
		}

		if role == "system" {
			if seenSystem {
				role = "user"
				content = "[system]\n" + content
			}
			seenSystem = true
		}
		if prefix := UserMessagePrefix(m); prefix != "" {
			content = prefix + m.Content
		}

		var toolCalls []providers.ToolCall
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			allPresent := true
			for _, tc := range m.ToolCalls {
				if !toolResponses[tc.ID] {
					allPresent = false
					break
				}
			}
			if allPresent {
				toolCalls = m.ToolCalls
			}
		} else {
			toolCalls = m.ToolCalls
		}

		var msgContent any = content
		if visionCapable && len(m.ImageURLs) > 0 && role == "user" {
			parts := []providers.ContentPart{
				{Type: "text", Text: content},
			}
			for _, u := range m.ImageURLs {
				parts = append(parts, providers.ContentPart{
					Type:     "image_url",
					ImageURL: &providers.ImageURL{URL: u},
				})
			}
			msgContent = parts
		}

		out = append(out, providers.Message{
			Role:       role,
			Content:    msgContent,
			ToolCalls:  toolCalls,
			ToolCallID: m.ToolCallID,
		})
	}
	return out
}

// ConvertTools は suzuha Tool を providers.Tool に変換する。
func ConvertTools(tools []tool.Tool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]providers.Tool, 0, len(tools))
	for _, t := range tools {
		var params map[string]any
		if err := json.Unmarshal(t.InputSchema(), &params); err != nil {
			slog.Warn("llmconv: ツールの入力スキーマのパースに失敗", "tool", t.Name(), "error", err)
			continue
		}
		out = append(out, providers.Tool{
			Type: "function",
			Function: providers.Function{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return out
}

// UserMessagePrefix は user ロールのメッセージに付与するメタデータヘッダを返す。
// MessageID が空なら空文字を返す。ConvertMessages と lib/llmtrace の両方から使われる。
func UserMessagePrefix(m message.Message) string {
	if m.Role != "user" || m.MessageID == "" {
		return ""
	}
	ts := ""
	if !m.Timestamp.IsZero() {
		ts = jtime.In(m.Timestamp).Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("[time=%s guild=%s guild_id=%s channel=#%s channel_id=%s message_id=%s platform=%s user_id=%s user=%s]\n",
		ts, m.GuildName, m.GuildID, m.ChannelName, m.Channel, m.MessageID, m.Source, m.UserID, m.UserName)
}
