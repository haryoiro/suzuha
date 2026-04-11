package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// skipResponseTool is a virtual tool that signals the LLM wants to skip responding.
// It is NOT registered in the global tool.Registry — it is injected per-call
// into completeWithTools only when the directive allows skipping.
type skipResponseTool struct{}

func (skipResponseTool) Name() string    { return "skip_response" }
func (skipResponseTool) ReadOnly() bool { return true }
func (skipResponseTool) Description() string {
	return "この会話に返答しないときに呼ぶ。重要: テキストを返すなら絶対にこのツールを呼ばないこと（テキストとskip_responseの同時使用は禁止）。discord_react と一緒に呼んでもよい。"
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
	resp, intermediateText, err := a.completeWithToolsUsing(ctx, agentCtx, sess, t, p.Channel, p.LastMessage.ChannelName)
	if err != nil {
		return "", fmt.Errorf("agent: 補完に失敗: %w", err)
	}

	// Add assistant response to context.
	// When the response has tool calls, the assistant message was already added
	// inside completeWithToolsUsing (before executing the tools).
	if !resp.HasToolCalls() {
		agentCtx.Add(llm.Message{
			Role:        "assistant",
			Content:     resp.Text,
			Channel:     p.Channel,
			ChannelName: p.LastMessage.ChannelName,
			Timestamp:   jtime.Now(),
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
			"raw_text", textutil.TruncateRunes(resp.Text, 100))
		return "", nil
	case a.isDuplicateResponse(p.Channel, text):
		a.logger.Info("同じこと言いそうなので黙った (dedup)",
			"channel", p.Channel, "length", len(text))
		return "", nil
	default:
		return text, nil
	}
}

// completeWithToolsUsing runs the LLM and executes tool calls in a loop,
// using the given agent context and session for typing/intermediate responses.
func (a *Agent) completeWithToolsUsing(ctx context.Context, agentCtx *Context, sess Session, t *Thought, channel, channelName string) (*llm.Response, string, error) {
	directive := t.Directive
	allTools := a.tools.AllEnabled()

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
	var lowProgressStreak int // 連続して出力が少ない回数 (diminishing returns 検知用)

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
			msgs = t.BuildMessages(agentCtx.SystemPrompt(), agentCtx.Messages())
		} else {
			msgs = agentCtx.MessagesWithSystem()
		}
		// Trim messages to fit within max context, reserving space for tools.
		maxCtx := a.llm.MaxContextTokens()
		msgs = trimMessagesToFit(msgs, allTools, maxCtx)

		// Reactive Compact: ツールループ中にコンテキストが 90% を超えたら
		// 緊急圧縮してコンテキストを解放する。
		if iter > 0 && maxCtx > 0 {
			estimated := agentCtx.EstimatedTokens()
			if float64(estimated)/float64(maxCtx) > 0.9 {
				a.logger.Warn("ツールループ中にコンテキスト逼迫、緊急圧縮",
					"usage_ratio", fmt.Sprintf("%.2f", float64(estimated)/float64(maxCtx)),
					"estimated", estimated, "max", maxCtx)
				a.compactAsyncFor(ctx, agentCtx, sourceKeyFromChannel(channel, a.contexts))
				// 圧縮後のメッセージで再構築
				msgs = agentCtx.MessagesWithSystem()
				msgs = trimMessagesToFit(msgs, allTools, maxCtx)
			}
		}

		// チャンネルごとにグルーピングし、現チャンネルを末尾に寄せる。
		// LLM の recency bias を活かして現チャンネルの会話にフォーカスさせる。
		if channel != "" {
			msgs = groupByChannel(msgs, channel)
		}

		rc := a.llm.For("conversation")
		provMsgs := llm.ConvertMessages(msgs, rc.HasCapability("vision"))
		provTools := llm.ConvertTools(allTools)

		var span trace.Span
		if a.tracer != nil {
			ctx, span = a.tracer.Start(ctx, "llm.complete",
				trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(
					attribute.String("gen_ai.system", rc.ProviderName()),
					attribute.String("gen_ai.request.model", rc.Model()),
					attribute.Int("gen_ai.prompt.message_count", len(msgs)),
					attribute.Int("gen_ai.request.tool_count", len(allTools)),
				),
			)
			defer span.End()
		}

		resp, err := rc.CompleteWithTools(ctx, provMsgs, provTools)
		if err != nil {
			if span != nil {
				span.RecordError(err)
			}
			return nil, intermediateText, err
		}

		if span != nil {
			span.SetAttributes(
				attribute.Int("gen_ai.usage.prompt_tokens", resp.Usage.PromptTokens),
				attribute.Int("gen_ai.usage.completion_tokens", resp.Usage.CompletionTokens),
				attribute.String("gen_ai.response.finish_reason", resp.FinishReason),
				attribute.Int("gen_ai.response.tool_call_count", len(resp.ToolCalls)),
			)
		}

		a.logger.Info("考えた",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", textutil.TruncateRunes(resp.Text, 200))

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

		// Diminishing returns detection: 出力トークンが少ない iteration が
		// 3 回以上続いたらツールループを打ち切る (空回り防止)。
		if resp.Usage.CompletionTokens < 500 {
			lowProgressStreak++
		} else {
			lowProgressStreak = 0
		}
		if lowProgressStreak >= 3 {
			a.logger.Warn("ツールループの進捗が停滞、打ち切り",
				"iteration", iter, "low_progress_streak", lowProgressStreak)
			return resp, intermediateText, nil
		}

		// skip_response が含まれる場合、コンテキストに何も残さない。
		// 副作用ツール (discord_react 等) だけ実行して即終了。
		if containsSkipTool(resp.ToolCalls) {
			for _, tc := range resp.ToolCalls {
				if tc.Function.Name == "skip_response" {
					continue
				}
				if t, ok := toolMap[tc.Function.Name]; ok {
					if _, err := t.Execute(ctx, json.RawMessage(tc.Function.Arguments)); err != nil {
						a.logger.Warn("skip中のツール失敗", "tool", tc.Function.Name, "error", err)
					}
				}
			}
			return resp, intermediateText, nil
		}

		agentCtx.Add(llm.Message{
			Role:        "assistant",
			Content:     resp.Text,
			Channel:     channel,
			ChannelName: channelName,
			Timestamp:   jtime.Now(),
			ToolCalls:   resp.ToolCalls,
		})

		// Send intermediate text if the LLM returned text alongside tool calls.
		if stripped := llm.StripDirectiveTags(resp.Text); stripped != "" && channel != "" {
			a.logger.Info("途中で話した",
				"channel", channel, "length", len(stripped))
			if err := sess.Respond(ctx, stripped); err != nil {
				a.logger.Warn("途中の発言に失敗", "error", err)
			}
			intermediateText = stripped
		}

		allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, toolMap, resp.ToolCalls, channel, iter)

		if allStopAfter {
			a.logger.Info("ツールが完了した", "iteration", iter)
			return resp, intermediateText, nil
		}
	}

	return nil, intermediateText, fmt.Errorf("agent: ツールループが %d 回の反復を超過しました", maxIter)
}
