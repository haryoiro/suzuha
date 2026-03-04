package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
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

// Act runs the LLM completion with tool loop, filters the response,
// and sends it to chat.
func (a *Agent) Act(ctx context.Context, p *Perception, t *Thought) error {
	resp, err := a.completeWithTools(ctx, t.Directive, p.Channel, t.Ephemeral)
	if err != nil {
		return fmt.Errorf("agent: complete: %w", err)
	}

	// Add assistant response to context.
	// When the response has tool calls, the assistant message was already added
	// inside completeWithTools (before executing the tools).
	if !resp.HasToolCalls() {
		a.ctx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   p.Channel,
			Timestamp: time.Now(),
		})
	}

	// Send response (strip directive tags and silent markers).
	// Think tags are already parsed in llm.Complete().
	text := llm.StripDirectiveTags(resp.Text)
	switch {
	case containsSkipTool(resp.ToolCalls) && text == "":
		a.logger.Info("skipping response (skip_response tool)")
	case containsSkipTool(resp.ToolCalls) && text != "":
		a.logger.Warn("skip_response called with text, sending anyway",
			"text_length", len(text))
		if err := a.chat.Send(ctx, p.Channel, text); err != nil {
			return fmt.Errorf("agent: send: %w", err)
		}
	case llm.IsSilentResponse(text):
		a.logger.Info("skipping response (silent)",
			"raw_text", truncate(resp.Text, 100))
	case a.channelSettings != nil && p.Channel != "" && !p.IsDM &&
		a.channelSettings.GetMode(p.Channel) != channelpkg.ModeActive:
		a.logger.Info("suppressing send to non-active channel",
			"channel", p.Channel, "mode", string(a.channelSettings.GetMode(p.Channel)))
	default:
		a.logger.Info("sending response",
			"channel", p.Channel,
			"length", len(text),
			"content", truncate(text, 200))
		if err := a.chat.Send(ctx, p.Channel, text); err != nil {
			return fmt.Errorf("agent: send: %w", err)
		}
	}

	return nil
}

// completeWithTools runs the LLM and executes tool calls in a loop.
func (a *Agent) completeWithTools(ctx context.Context, directive, channel string, ephemeral []llm.Message) (*llm.Response, error) {
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

	for iter := range maxIter {
		if channel != "" {
			if typer, ok := a.chat.(chat.Typer); ok {
				typer.Typing(ctx, channel)
			}
		}

		msgs := a.ctx.MessagesWithSystem()
		if iter == 0 {
			msgs = append(msgs, ephemeral...)
			now := time.Now()
			msgs = append(msgs, llm.Message{
				Role:      "system",
				Content:   fmt.Sprintf("[現在時刻: %s]", now.Format("2006-01-02 15:04:05 (Mon)")),
				Timestamp: now,
			})
			if directive != "" {
				msgs = append(msgs, llm.Message{
					Role:      "system",
					Content:   directive,
					Timestamp: now,
				})
			}
		}
		// Trim messages to fit within max context, reserving space for tools.
		msgs = trimMessagesToFit(msgs, allTools, a.llm.MaxContextTokens())

		resp, err := a.llm.Complete(ctx, msgs, allTools)
		if err != nil {
			return nil, err
		}

		a.logger.Info("llm response",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", truncate(resp.Text, 200))

		// Calibrate token estimator using actual prompt tokens from the provider.
		if iter == 0 && resp.Usage.PromptTokens > 0 {
			a.ctx.CalibrateTokens(resp.Usage.PromptTokens)
			a.logger.Debug("token calibration updated",
				"actual", resp.Usage.PromptTokens,
				"estimated", a.ctx.EstimatedTokens(),
				"ratio", fmt.Sprintf("%.2f", a.ctx.TokenCalibration()))
		}

		if !resp.HasToolCalls() {
			return resp, nil
		}

		a.ctx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   channel,
			Timestamp: time.Now(),
			ToolCalls: resp.ToolCalls,
		})

		// Send intermediate text to chat if the LLM returned text alongside tool calls.
		if intermediateText := llm.StripDirectiveTags(resp.Text); intermediateText != "" && channel != "" && !containsSkipTool(resp.ToolCalls) {
			a.logger.Info("sending intermediate response before tool execution",
				"channel", channel, "length", len(intermediateText))
			if err := a.chat.Send(ctx, channel, intermediateText); err != nil {
				a.logger.Warn("failed to send intermediate response", "error", err)
			}
		}

		allStopAfter := true
		for _, tc := range resp.ToolCalls {
			a.logger.Info("tool call",
				"iteration", iter,
				"tool", tc.Function.Name,
				"call_id", tc.ID,
				"args", truncate(tc.Function.Arguments, 200))

			t, ok := toolMap[tc.Function.Name]
			if !ok {
				a.logger.Warn("unknown tool", "tool", tc.Function.Name)
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Function.Name),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
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
				a.logger.Error("tool execute error",
					"tool", tc.Function.Name, "error", err, "elapsed_ms", elapsed.Milliseconds())
				if a.metrics != nil {
					a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "error").Inc()
				}
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: %v", err),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
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

			a.logger.Info("tool result",
				"tool", tc.Function.Name,
				"elapsed_ms", elapsed.Milliseconds(),
				"is_error", result.IsError,
				"result", truncate(content, 200))

			a.ctx.Add(llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now(),
			})
		}

		if allStopAfter {
			a.logger.Info("all tools returned StopAfter, ending tool loop", "iteration", iter)
			return resp, nil
		}
	}

	return nil, fmt.Errorf("agent: tool loop exceeded %d iterations", maxIter)
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

