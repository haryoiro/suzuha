package llm

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// ConvertMessages transforms suzuha Messages to any-llm-go Messages.
// System messages after the first one are converted to user messages,
// because some models (e.g. Qwen3.5) only allow a single system message at the start.
// Orphaned tool messages and unmatched tool_calls are sanitized to satisfy
// strict providers like OpenAI.
func ConvertMessages(msgs []Message, visionCapable bool) []providers.Message {
	// Collect tool_call IDs that have assistant requests and tool responses.
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

		// Drop orphaned tool responses (no matching assistant tool_calls).
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
		// Embed message metadata so the LLM can identify channel context.
		if m.Role == "user" && m.MessageID != "" {
			ts := ""
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format("2006-01-02 15:04")
			}
			content = fmt.Sprintf("[time=%s server=%s channel=#%s channel_id=%s guild_id=%s message_id=%s platform=%s user_id=%s user=%s]\n%s",
				ts, m.GuildName, m.ChannelName, m.Channel, m.GuildID, m.MessageID, m.Source, m.UserID, m.UserName, m.Content)
		}
		// assistant メッセージには channel 名だけを最小限付与。
		// フルメタデータを付けると LLM がフォーマットを真似るため、チャンネル名のみ。
		if m.Role == "assistant" && m.Channel != "" && m.ChannelName != "" {
			content = fmt.Sprintf("[channel=#%s]\n%s", m.ChannelName, content)
		}

		// Strip tool_calls from assistant messages if any response is missing.
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
			// else: drop tool_calls entirely — responses are missing
		} else {
			toolCalls = m.ToolCalls
		}

		// Build multimodal content if the LLM supports vision and there are images.
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

// ConvertTools transforms suzuha Tool interfaces to any-llm-go Tool structs.
func ConvertTools(tools []tool.Tool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]providers.Tool, 0, len(tools))
	for _, t := range tools {
		var params map[string]any
		if err := json.Unmarshal(t.InputSchema(), &params); err != nil {
			slog.Warn("llm: ツールの入力スキーマのパースに失敗", "tool", t.Name(), "error", err)
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
