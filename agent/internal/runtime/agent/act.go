package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/port/tool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

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

// llmRoleForPerception は Perception に基づいて LLM ロールを決定する。
// voice 会話には "voice" ロール (glm-4.7 等の高速モデル) を使用し、
// テキスト会話には "conversation" ロール (glm-5.1 等の高精度モデル) を使用する。
func llmRoleForPerception(p *Perception) string {
	if p.IsVoice {
		return "voice"
	}
	return "conversation"
}

// ActWith runs the LLM completion with tool loop, filters the response,
// and returns the response text. It does NOT route the response to any output;
// the caller is responsible for sending the response via Session.Respond().
func (a *Agent) ActWith(ctx context.Context, agentCtx *Context, sess Session, p *Perception, t *Thought) (string, error) {
	llmRole := llmRoleForPerception(p)
	resp, err := a.completeWithToolsUsing(ctx, agentCtx, sess, t, p.Channel, p.LastMessage.ChannelName, llmRole)
	if err != nil {
		return "", fmt.Errorf("agent: 補完に失敗: %w", err)
	}

	// Add assistant response to context.
	// When the response has tool calls, the assistant message was already added
	// inside completeWithToolsUsing (before executing the tools).
	if !resp.HasToolCalls() {
		agentCtx.Add(assistantMessage(resp.Text, p.Channel, p.LastMessage.ChannelName, nil))
	}

	return a.filterResponse(resp, p.Channel), nil
}

// filterResponse はレスポンスを検査し、送信すべきテキストを返す。
// silent / dedup のいずれかに該当する場合は空文字を返す。
// skip_response は text が空のときだけ沈黙として扱う (text 共存時は text 優先)。
func (a *Agent) filterResponse(resp *llm.Response, channel string) string {
	text := strings.TrimSpace(llm.StripDirectiveTags(resp.Text))
	skip := containsSkipTool(resp.ToolCalls)
	switch {
	case text == "":
		if skip {
			a.logger.Info("黙った (skip_response)")
		} else {
			a.logger.Debug("何も思いつかなかった")
		}
		return ""
	case llm.IsSilentResponse(text):
		a.logger.Info("黙った (サイレント)",
			"raw_text", textutil.TruncateRunes(resp.Text, 100))
		return ""
	case a.isDuplicateResponse(channel, text):
		a.logger.Info("同じこと言いそうなので黙った (dedup)",
			"channel", channel, "length", len(text))
		return ""
	default:
		if skip {
			a.logger.Warn("skip_response と text を同時生成、text を優先",
				"text_len", len(text))
		}
		return text
	}
}

// toolSet は LLM 呼び出しに使うツール一式を保持する。
type toolSet struct {
	tools   []tool.Tool
	toolMap map[string]tool.Tool
}

// prepareTools は directive に基づいてツール一式を準備する。
// [RESPOND] 以外の directive では skip_response ツールを注入する。
func (a *Agent) prepareTools(directive string) toolSet {
	tools := a.tools.AllEnabled()
	if !strings.HasPrefix(directive, "[RESPOND]") {
		tools = append(tools, skipResponseTool{})
	}
	m := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return toolSet{tools: tools, toolMap: m}
}

// buildIterMessages はイテレーション用のメッセージリストを構築する。
// iter=0 では Thought からビルド、iter>0 ではコンテキストのメッセージを使用する。
// reactive compact やチャンネルグルーピングも含む。
func (a *Agent) buildIterMessages(
	ctx context.Context,
	agentCtx *Context,
	t *Thought,
	ts toolSet,
	maxCtx int,
	channel string,
	iter int,
) []message.Message {
	var msgs []message.Message
	if iter == 0 {
		msgs = t.BuildMessages(agentCtx.SystemPrompt(), agentCtx.Messages())
	} else {
		msgs = agentCtx.MessagesWithSystem()
	}
	msgs = trimMessagesToFit(msgs, ts.tools, maxCtx)

	// Reactive Compact: ツールループ中にコンテキストが 90% を超えたら
	// 緊急圧縮してコンテキストを解放する。
	if iter > 0 && maxCtx > 0 {
		estimated := agentCtx.EstimatedTokens()
		if float64(estimated)/float64(maxCtx) > 0.9 {
			a.logger.Warn("ツールループ中にコンテキスト逼迫、緊急圧縮",
				"usage_ratio", fmt.Sprintf("%.2f", float64(estimated)/float64(maxCtx)),
				"estimated", estimated, "max", maxCtx)
			a.compactAsyncFor(ctx, agentCtx, sourceKeyFromChannel(channel, a.contexts))
			msgs = agentCtx.MessagesWithSystem()
			msgs = trimMessagesToFit(msgs, ts.tools, maxCtx)
		}
	}

	if channel != "" {
		msgs = groupByChannel(msgs, channel)
	}
	return msgs
}

// completeWithToolsUsing は LLM 補完 + ツールループを実行する。
// 初回 (iter=0) のトレーシングとキャリブレーションを行い、
// ツール呼び出しがあれば continueToolLoop に委譲する。
func (a *Agent) completeWithToolsUsing(ctx context.Context, agentCtx *Context, sess Session, t *Thought, channel, channelName, llmRole string) (*llm.Response, error) {
	ts := a.prepareTools(t.Directive)
	rc := a.llm.For(llmRole)

	msgs := a.buildIterMessages(ctx, agentCtx, t, ts, rc.MaxContextTokens(), channel, 0)
	provMsgs := llm.ConvertMessages(msgs, rc.HasCapability("vision"))
	provTools := llm.ConvertTools(ts.tools)

	var span trace.Span
	if a.tracer != nil {
		ctx, span = a.tracer.Start(ctx, "llm.complete",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("gen_ai.system", rc.ProviderName()),
				attribute.String("gen_ai.request.model", rc.Model()),
				attribute.Int("gen_ai.prompt.message_count", len(msgs)),
				attribute.Int("gen_ai.request.tool_count", len(ts.tools)),
				attribute.String("gen_ai.input", llm.SerializeMessagesForTrace(msgs)),
			),
		)
		defer span.End()
	}

	resp, err := rc.CompleteWithTools(ctx, provMsgs, provTools)
	if err != nil {
		if span != nil {
			span.RecordError(err)
		}
		return nil, err
	}

	if span != nil {
		span.SetAttributes(
			attribute.Int("gen_ai.usage.prompt_tokens", resp.Usage.PromptTokens),
			attribute.Int("gen_ai.usage.completion_tokens", resp.Usage.CompletionTokens),
			attribute.String("gen_ai.response.finish_reason", resp.FinishReason),
			attribute.Int("gen_ai.response.tool_call_count", len(resp.ToolCalls)),
			attribute.String("gen_ai.output", llm.SerializeResponseForTrace(resp)),
		)
	}

	a.logger.Info("考えた",
		"iteration", 0,
		"finish_reason", resp.FinishReason,
		"text_length", len(resp.Text),
		"tool_calls", len(resp.ToolCalls),
		"tokens_in", resp.Usage.PromptTokens,
		"tokens_out", resp.Usage.CompletionTokens,
		"content", textutil.TruncateRunes(resp.Text, 200))

	if resp.Usage.PromptTokens > 0 {
		agentCtx.CalibrateTokens(resp.Usage.PromptTokens)
		a.logger.Debug("トークン計算を補正",
			"actual", resp.Usage.PromptTokens,
			"estimated", agentCtx.EstimatedTokens(),
			"ratio", fmt.Sprintf("%.2f", agentCtx.TokenCalibration()))
	}

	if !resp.HasToolCalls() {
		return resp, nil
	}

	if containsSkipTool(resp.ToolCalls) {
		execSideEffectsOnSkip(ctx, ts.toolMap, resp.ToolCalls, a.logger)
		// skip_response と text を同時生成したケース: text を発言として採用し、
		// tool_calls を剥がして context に記録する (filterResponse が text を返す)。
		if resp.Text != "" {
			agentCtx.Add(assistantMessage(resp.Text, channel, channelName, nil))
		}
		return resp, nil
	}

	agentCtx.Add(assistantMessage(resp.Text, channel, channelName, resp.ToolCalls))

	allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, ts.toolMap, resp.ToolCalls, channel, 0)
	if allStopAfter {
		a.logger.Info("ツールが完了した", "iteration", 0)
		return resp, nil
	}

	return a.continueToolLoop(ctx, agentCtx, sess, t, ts, rc, channel, channelName)
}

// continueToolLoop はツールループのイテレーション 1 以降を実行する。
// completeWithToolsUsing と handleToolLoopFallback の両方から使われる。
func (a *Agent) continueToolLoop(
	ctx context.Context,
	agentCtx *Context,
	sess Session,
	t *Thought,
	ts toolSet,
	rc *llm.RoleClient,
	channel, channelName string,
) (*llm.Response, error) {
	maxCtx := rc.MaxContextTokens()
	const maxIter = 10
	var lowProgressStreak int

	for iter := 1; iter < maxIter; iter++ {
		if channel != "" {
			if ts, ok := sess.(typingSession); ok {
				ts.Typing(ctx)
			}
		}

		msgs := a.buildIterMessages(ctx, agentCtx, t, ts, maxCtx, channel, iter)
		provMsgs := llm.ConvertMessages(msgs, rc.HasCapability("vision"))
		provTools := llm.ConvertTools(ts.tools)

		resp, err := rc.CompleteWithTools(ctx, provMsgs, provTools)
		if err != nil {
			return nil, err
		}

		a.logger.Info("考えた",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", textutil.TruncateRunes(resp.Text, 200))

		if !resp.HasToolCalls() {
			return resp, nil
		}

		if resp.Usage.CompletionTokens < 500 {
			lowProgressStreak++
		} else {
			lowProgressStreak = 0
		}
		if lowProgressStreak >= 3 {
			a.logger.Warn("ツールループの進捗が停滞、打ち切り",
				"iteration", iter, "low_progress_streak", lowProgressStreak)
			return resp, nil
		}

		if containsSkipTool(resp.ToolCalls) {
			execSideEffectsOnSkip(ctx, ts.toolMap, resp.ToolCalls, a.logger)
			if resp.Text != "" {
				agentCtx.Add(assistantMessage(resp.Text, channel, channelName, nil))
			}
			return resp, nil
		}

		agentCtx.Add(assistantMessage(resp.Text, channel, channelName, resp.ToolCalls))

		allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, ts.toolMap, resp.ToolCalls, channel, iter)
		if allStopAfter {
			a.logger.Info("ツールが完了した", "iteration", iter)
			return resp, nil
		}
	}

	return nil, fmt.Errorf("agent: ツールループが %d 回の反復を超過しました", maxIter)
}
