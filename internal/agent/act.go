package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agnivade/levenshtein"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// skipResponseTool is a virtual tool that signals the LLM wants to skip responding.
// It is NOT registered in the global tool.Registry — it is injected per-call
// into completeWithTools only when the directive allows skipping.
type skipResponseTool struct{}

func (skipResponseTool) Name() string { return "skip_response" }
func (skipResponseTool) Description() string {
	return "この会話に返答しないときに呼ぶ。テキストを返す場合はこのツールを呼ばないこと。discord_react と一緒に呼んでもよい。"
}
func (skipResponseTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"スキップ理由（ログ用）"}},"required":[]}`)
}
func (skipResponseTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return tool.StopResult("skipped"), nil
}

var _ tool.Tool = skipResponseTool{}

// Act is the backward-compatible wrapper that calls ActWith with the discord context
// and session, then routes the response through the discord session.
func (a *Agent) Act(ctx context.Context, p *Perception, t *Thought) error {
	sess := a.sessions[SourceKeyDiscord]
	sess.BeginTurn(p)
	text, err := a.ActWith(ctx, a.contexts[SourceKeyDiscord], sess, p, t)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	return sess.Respond(ctx, text)
}

// ActWith runs the LLM completion with tool loop, filters the response,
// and returns the response text. It does NOT route the response to any output;
// the caller is responsible for sending the response via Session.Respond().
func (a *Agent) ActWith(ctx context.Context, agentCtx *Context, sess Session, p *Perception, t *Thought) (string, error) {
	resp, intermediateText, err := a.completeWithToolsUsing(ctx, agentCtx, sess, t.Directive, p.Channel, t.Ephemeral)
	if err != nil {
		return "", fmt.Errorf("agent: 補完に失敗: %w", err)
	}

	// Add assistant response to context.
	// When the response has tool calls, the assistant message was already added
	// inside completeWithToolsUsing (before executing the tools).
	if !resp.HasToolCalls() {
		agentCtx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   p.Channel,
			Timestamp: jtime.Now(),
		})
	}

	// Send response (strip directive tags and silent markers).
	// Think tags are already parsed in llm.Complete().
	text := strings.TrimSpace(llm.StripDirectiveTags(resp.Text))
	switch {
	case text == "":
		a.logger.Debug("何も思いつかなかった")
		return "", nil
	case containsSkipTool(resp.ToolCalls):
		a.logger.Info("黙った (skip_response)",
			"had_text", text != "")
		return "", nil
	case intermediateText != "" && isSimilarText(intermediateText, text):
		a.logger.Info("同じこと言いそうなので黙った",
			"intermediate_length", len(intermediateText),
			"final_length", len(text))
		return "", nil
	case llm.IsSilentResponse(text):
		a.logger.Info("黙った (サイレント)",
			"raw_text", truncate(resp.Text, 100))
		return "", nil
	default:
		return text, nil
	}
}

// completeWithTools is the backward-compatible wrapper.
func (a *Agent) completeWithTools(ctx context.Context, directive, channel string, ephemeral []llm.Message) (*llm.Response, string, error) {
	return a.completeWithToolsUsing(ctx, a.contexts[SourceKeyDiscord], a.sessions[SourceKeyDiscord], directive, channel, ephemeral)
}

// completeWithToolsUsing runs the LLM and executes tool calls in a loop,
// using the given agent context and session for typing/intermediate responses.
func (a *Agent) completeWithToolsUsing(ctx context.Context, agentCtx *Context, sess Session, directive, channel string, ephemeral []llm.Message) (*llm.Response, string, error) {
	allTools := a.tools.AllEnabled()

	// Include skip_response tool when the directive allows skipping (not [RESPOND]).
	if !strings.HasPrefix(directive, "[RESPOND]") {
		allTools = append(allTools, skipResponseTool{})
	}

	// Build a local tool map for this call (includes injected skip_response).
	toolMap := make(map[string]tool.Tool, len(allTools))
	for _, t := range allTools {
		toolMap[t.Name()] = t
	}

	maxIter := 10
	var intermediateText string

	for iter := range maxIter {
		// Send typing indicator only on subsequent iterations (tool loops),
		// not on the first call where we don't yet know if we'll respond.
		if iter > 0 && channel != "" {
			if ds, ok := sess.(*DiscordSession); ok {
				ds.Typing(ctx)
			}
		}

		var msgs []llm.Message
		if iter == 0 {
			// Order: system prompt (with time) → ephemeral (profiles, memories) → conversation → directive
			// Ephemeral context before conversation lets the LLM read messages
			// with knowledge of who the users are and what it remembers.
			// Directive last for maximum recency effect.
			// Time is embedded in the system prompt so the LLM knows the time
			// without being tempted to report it.
			now := jtime.Now()
			sp := agentCtx.SystemPrompt()
			if sp != "" {
				sp += fmt.Sprintf("\n\n[現在時刻: %s]", now.Format("2006-01-02 15:04:05 (Mon)"))
				msgs = append(msgs, llm.Message{Role: "system", Content: sp})
			}
			msgs = append(msgs, ephemeral...)
			msgs = append(msgs, agentCtx.Messages()...)
			if directive != "" {
				msgs = append(msgs, llm.Message{
					Role:      "system",
					Content:   directive,
					Timestamp: now,
				})
			}
		} else {
			msgs = agentCtx.MessagesWithSystem()
		}
		// Trim messages to fit within max context, reserving space for tools.
		msgs = trimMessagesToFit(msgs, allTools, a.llm.MaxContextTokens())

		resp, err := a.llm.Complete(ctx, msgs, allTools)
		if err != nil {
			return nil, intermediateText, err
		}

		a.logger.Info("考えた",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", truncate(resp.Text, 200))

		// Calibrate token estimator using actual prompt tokens from the provider.
		if iter == 0 && resp.Usage.PromptTokens > 0 {
			agentCtx.CalibrateTokens(resp.Usage.PromptTokens)
			a.logger.Debug("トークン計算を補正",
				"actual", resp.Usage.PromptTokens,
				"estimated", agentCtx.EstimatedTokens(),
				"ratio", fmt.Sprintf("%.2f", agentCtx.TokenCalibration()))
		}

		if !resp.HasToolCalls() {
			return resp, intermediateText, nil
		}

		agentCtx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   channel,
			Timestamp: jtime.Now(),
			ToolCalls: resp.ToolCalls,
		})

		// Send intermediate text if the LLM returned text alongside tool calls.
		if stripped := llm.StripDirectiveTags(resp.Text); stripped != "" && channel != "" && !containsSkipTool(resp.ToolCalls) {
			a.logger.Info("途中で話した",
				"channel", channel, "length", len(stripped))
			if err := sess.Respond(ctx, stripped); err != nil {
				a.logger.Warn("途中の発言に失敗", "error", err)
			}
			intermediateText = stripped
		}

		allStopAfter := true
		for _, tc := range resp.ToolCalls {
			a.logger.Info("ツールを使う",
				"iteration", iter,
				"tool", tc.Function.Name,
				"call_id", tc.ID,
				"args", truncate(tc.Function.Arguments, 200))

			t, ok := toolMap[tc.Function.Name]
			if !ok {
				a.logger.Warn("知らないツールを呼ばれた", "tool", tc.Function.Name)
				agentCtx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: 不明なツール %q", tc.Function.Name),
					Channel:    channel,
					ToolCallID: tc.ID,
					Timestamp:  jtime.Now(),
				})
				allStopAfter = false
				continue
			}

			if a.metrics != nil {
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "called").Inc()
			}

			start := time.Now()
			result, err := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			elapsed := time.Since(start)

			if err != nil {
				a.logger.Error("ツールが失敗した",
					"tool", tc.Function.Name, "error", err, "elapsed_ms", elapsed.Milliseconds())
				if a.metrics != nil {
					a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "error").Inc()
				}
				agentCtx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: %v", err),
					Channel:    channel,
					ToolCallID: tc.ID,
					Timestamp:  jtime.Now(),
				})
				allStopAfter = false
				continue
			}

			if !result.StopAfter {
				allStopAfter = false
			}

			if a.metrics != nil {
				status := "success"
				if result.IsError {
					status = "error"
				}
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, status).Inc()
			}

			content := ""
			for _, c := range result.Content {
				content += c.Text
			}

			a.logger.Info("ツールの結果",
				"tool", tc.Function.Name,
				"elapsed_ms", elapsed.Milliseconds(),
				"is_error", result.IsError,
				"result", truncate(content, 200))

			agentCtx.Add(llm.Message{
				Role:       "tool",
				Content:    content,
				Channel:    channel,
				ToolCallID: tc.ID,
				Timestamp:  jtime.Now(),
			})

			// If the tool returned images, inject them as a user message
			// (OpenAI API only supports multimodal content in user messages).
			if len(result.ImageURLs) > 0 {
				agentCtx.Add(llm.Message{
					Role:      "user",
					Content:   content,
					ImageURLs: result.ImageURLs,
					Channel:   channel,
					Timestamp: jtime.Now(),
				})
			}
		}

		if allStopAfter {
			a.logger.Info("ツールが完了した", "iteration", iter)
			return resp, intermediateText, nil
		}
	}

	return nil, intermediateText, fmt.Errorf("agent: ツールループが %d 回の反復を超過しました", maxIter)
}

// isSimilarText returns true if two texts are similar enough to be considered duplicates.
// Used for intra-turn dedup (intermediate text vs final response). Threshold: 95%.
func isSimilarText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	dist := levenshtein.ComputeDistance(a, b)
	maxLen := max(len([]rune(a)), len([]rune(b)))
	return 1.0-float64(dist)/float64(maxLen) >= 0.95
}

// containsSkipTool returns true if the tool calls include skip_response.
func containsSkipTool(calls []providers.ToolCall) bool {
	for _, tc := range calls {
		if tc.Function.Name == "skip_response" {
			return true
		}
	}
	return false
}

// trimMessagesToFit drops the oldest non-system messages (from the front,
// after the first system message) so the total estimated tokens fit within
// maxTokens, leaving room for tool definitions and a generation budget.
func trimMessagesToFit(msgs []llm.Message, tools []tool.Tool, maxTokens int) []llm.Message {
	if maxTokens <= 0 {
		return msgs
	}

	// Estimate tool definition tokens from actual schema sizes.
	toolTokens := 0
	for _, t := range tools {
		// name + description + JSON schema + overhead
		toolTokens += estimateStringTokens(t.Name()) + estimateStringTokens(t.Description()) + estimateStringTokens(string(t.InputSchema())) + 20
	}
	// Reserve tokens for generation output.
	generationBudget := 512
	budget := maxTokens - toolTokens - generationBudget
	if budget < 500 {
		budget = 500
	}

	// Calculate total tokens.
	total := 0
	for _, m := range msgs {
		total += estimateStringTokens(m.Content) + 4
	}

	if total <= budget {
		return msgs
	}

	// Find the first non-system message index (skip leading system prompt).
	trimStart := 0
	for trimStart < len(msgs) && msgs[trimStart].Role == "system" {
		trimStart++
	}

	// Drop oldest conversation messages until we fit.
	for total > budget && trimStart < len(msgs)-1 {
		total -= estimateStringTokens(msgs[trimStart].Content) + 4
		trimStart++
	}

	// Rebuild: leading system messages + remaining messages.
	var leading int
	for leading < len(msgs) && msgs[leading].Role == "system" {
		leading++
	}
	result := make([]llm.Message, 0, leading+(len(msgs)-trimStart))
	result = append(result, msgs[:leading]...)
	result = append(result, msgs[trimStart:]...)
	return result
}
