package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// actStreamWith は voice セッション用のストリーミング Act。
// LLM ストリームを文単位に分割し、Session.RespondStream で逐次送信する。
// ツール呼び出しが発生した場合は非ストリーミングの completeWithToolsUsing にフォールバックする。
//
// 戻り値の string は agentCtx に追加済みのレスポンステキスト (ログ用)。
func (a *Agent) actStreamWith(ctx context.Context, agentCtx *Context, sess *DiscordSession, p *Perception, t *Thought) (string, error) {
	ts := a.prepareTools(t.Directive)
	rc := a.llm.For(llmRoleForPerception(p))
	maxCtx := rc.MaxContextTokens()

	// iter=0 のメッセージを構築。
	msgs := a.buildIterMessages(ctx, agentCtx, t, ts, maxCtx, p.Channel, 0)
	provMsgs := llm.ConvertMessages(msgs, rc.HasCapability("vision"))
	provTools := llm.ConvertTools(ts.tools)

	// Tracing span.
	var span trace.Span
	if a.tracer != nil {
		ctx, span = a.tracer.Start(ctx, "llm.stream",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("gen_ai.system", rc.ProviderName()),
				attribute.String("gen_ai.request.model", rc.Model()),
				attribute.Int("gen_ai.prompt.message_count", len(msgs)),
				attribute.Int("gen_ai.request.tool_count", len(ts.tools)),
			),
		)
		defer span.End()
	}

	chunks, streamErrs := rc.CompleteStreamWithTools(ctx, provMsgs, provTools)

	// SentenceBuffer → sentences チャネル。
	sentences := make(chan string, 4)
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var toolCallsDetected bool
	var finalChunk llm.StreamChunk

	// チャンク消費 goroutine。
	streamDone := make(chan error, 1)
	go func() {
		defer close(sentences)

		sb := textutil.NewSentenceBuffer(60, func(sentence string) {
			sentences <- sentence
		})

		for chunk := range chunks {
			if chunk.Done {
				finalChunk = chunk
				// ToolCall 検出。
				if len(chunk.ToolCalls) > 0 {
					toolCallsDetected = true
				}
				break
			}

			if chunk.Content != "" {
				contentBuf.WriteString(chunk.Content)
				sb.Write(chunk.Content)
			}
			if chunk.Reasoning != "" {
				reasoningBuf.WriteString(chunk.Reasoning)
			}
		}

		// ストリーム終了: バッファに残った文を flush。
		sb.Flush()
		streamDone <- nil
	}()

	// RespondStream はストリーミングで音声を送信する。
	// sentences チャネルがクローズされるまでブロックする。
	respondErr := sess.RespondStream(ctx, sentences)

	// チャンク消費 goroutine の完了を待つ。
	<-streamDone

	// Provider error check.
	if err, ok := <-streamErrs; ok && err != nil {
		if span != nil {
			span.RecordError(err)
		}
		return "", fmt.Errorf("agent: streaming 補完に失敗: %w", err)
	}

	if respondErr != nil {
		return "", fmt.Errorf("agent: ストリーミング応答に失敗: %w", respondErr)
	}

	fullText := contentBuf.String()

	// Tracing attributes.
	if span != nil && finalChunk.Usage != nil {
		span.SetAttributes(
			attribute.Int("gen_ai.usage.prompt_tokens", finalChunk.Usage.PromptTokens),
			attribute.Int("gen_ai.usage.completion_tokens", finalChunk.Usage.CompletionTokens),
			attribute.String("gen_ai.response.finish_reason", finalChunk.FinishReason),
		)
	}

	a.logger.Info("考えた (streaming)",
		"finish_reason", finalChunk.FinishReason,
		"text_length", len(fullText),
		"tool_calls", len(finalChunk.ToolCalls),
		"content", textutil.TruncateRunes(fullText, 200))

	// Token calibration (iter=0).
	if finalChunk.Usage != nil && finalChunk.Usage.PromptTokens > 0 {
		agentCtx.CalibrateTokens(finalChunk.Usage.PromptTokens)
	}

	// ToolCall フォールバック: ストリーミングで ToolCall が検出された場合、
	// 蓄積したレスポンスをコンテキストに追加してバッチの tool loop に引き継ぐ。
	if toolCallsDetected {
		a.logger.Info("ストリーミング中にツール呼び出し検出、バッチにフォールバック")

		// ストリーミングで得たレスポンスを Response に変換してバッチパスに渡す。
		resp := &llm.Response{
			Text:         fullText,
			Reasoning:    reasoningBuf.String(),
			ToolCalls:    finalChunk.ToolCalls,
			FinishReason: finalChunk.FinishReason,
		}
		if finalChunk.Usage != nil {
			resp.Usage = *finalChunk.Usage
		}

		// completeWithToolsUsing の tool loop 部分を手動で実行。
		return a.handleToolLoopFallback(ctx, agentCtx, sess, t, ts, rc, resp, fullText, p.Channel, p.LastMessage.ChannelName)
	}

	// ツールなし: コンテキストに追加。
	agentCtx.Add(llm.Message{
		Role:        "assistant",
		Content:     fullText,
		Channel:     p.Channel,
		ChannelName: p.LastMessage.ChannelName,
		Timestamp:   jtime.Now(),
	})

	return a.filterResponse(&llm.Response{Text: fullText, ToolCalls: finalChunk.ToolCalls}, "", p.Channel), nil
}

// handleToolLoopFallback はストリーミングで ToolCall を検出した後、
// バッチモードの tool loop を実行する。
func (a *Agent) handleToolLoopFallback(
	ctx context.Context,
	agentCtx *Context,
	sess *DiscordSession,
	t *Thought,
	ts toolSet,
	rc *llm.RoleClient,
	firstResp *llm.Response,
	intermediateText string,
	channel, channelName string,
) (string, error) {
	maxCtx := rc.MaxContextTokens()

	// 最初のレスポンスをコンテキストに追加。
	agentCtx.Add(llm.Message{
		Role:        "assistant",
		Content:     firstResp.Text,
		Channel:     channel,
		ChannelName: channelName,
		Timestamp:   jtime.Now(),
		ToolCalls:   firstResp.ToolCalls,
	})

	// skip_response チェック。
	if containsSkipTool(firstResp.ToolCalls) {
		for _, tc := range firstResp.ToolCalls {
			if tc.Function.Name == "skip_response" {
				continue
			}
			if tool, ok := ts.toolMap[tc.Function.Name]; ok {
				if _, err := tool.Execute(ctx, []byte(tc.Function.Arguments)); err != nil {
					a.logger.Warn("skip中のツール失敗", "tool", tc.Function.Name, "error", err)
				}
			}
		}
		return "", nil
	}

	// ツール実行。
	allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, ts.toolMap, firstResp.ToolCalls, channel, 0)
	if allStopAfter {
		return a.filterResponse(firstResp, intermediateText, channel), nil
	}

	// 残りのイテレーション (バッチモード)。
	maxIter := 10
	var lowProgressStreak int

	for iter := 1; iter < maxIter; iter++ {
		if channel != "" {
			sess.Typing(ctx)
		}

		msgs := a.buildIterMessages(ctx, agentCtx, t, ts, maxCtx, channel, iter)
		provMsgs := llm.ConvertMessages(msgs, rc.HasCapability("vision"))
		provTools := llm.ConvertTools(ts.tools)

		resp, err := rc.CompleteWithTools(ctx, provMsgs, provTools)
		if err != nil {
			return "", err
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
			agentCtx.Add(llm.Message{
				Role:        "assistant",
				Content:     resp.Text,
				Channel:     channel,
				ChannelName: channelName,
				Timestamp:   jtime.Now(),
			})
			return a.filterResponse(resp, intermediateText, channel), nil
		}

		if resp.Usage.CompletionTokens < 500 {
			lowProgressStreak++
		} else {
			lowProgressStreak = 0
		}
		if lowProgressStreak >= 3 {
			a.logger.Warn("ツールループの進捗が停滞、打ち切り",
				"iteration", iter, "low_progress_streak", lowProgressStreak)
			return a.filterResponse(resp, intermediateText, channel), nil
		}

		if containsSkipTool(resp.ToolCalls) {
			for _, tc := range resp.ToolCalls {
				if tc.Function.Name == "skip_response" {
					continue
				}
				if tool, ok := ts.toolMap[tc.Function.Name]; ok {
					if _, err := tool.Execute(ctx, []byte(tc.Function.Arguments)); err != nil {
						a.logger.Warn("skip中のツール失敗", "tool", tc.Function.Name, "error", err)
					}
				}
			}
			return "", nil
		}

		agentCtx.Add(llm.Message{
			Role:        "assistant",
			Content:     resp.Text,
			Channel:     channel,
			ChannelName: channelName,
			Timestamp:   jtime.Now(),
			ToolCalls:   resp.ToolCalls,
		})

		if stripped := llm.StripDirectiveTags(resp.Text); stripped != "" && channel != "" {
			if err := sess.Respond(ctx, stripped); err != nil {
				a.logger.Warn("途中の発言に失敗", "error", err)
			}
			intermediateText = stripped
		}

		allStopAfter = a.executeToolCalls(ctx, agentCtx, sess, ts.toolMap, resp.ToolCalls, channel, iter)
		if allStopAfter {
			return a.filterResponse(resp, intermediateText, channel), nil
		}
	}

	return "", fmt.Errorf("agent: ツールループが %d 回の反復を超過しました", maxIter)
}

