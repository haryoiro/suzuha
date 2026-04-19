package agent

import (
	"context"
	"fmt"
	"strings"

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
func (a *Agent) actStreamWith(ctx context.Context, agentCtx *Context, sess StreamingSession, p *Perception, t *Thought) (string, error) {
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
		ctx, span = a.tracer.Start(ctx, "llm.complete",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("gen_ai.system", rc.ProviderName()),
				attribute.String("gen_ai.request.model", rc.Model()),
				attribute.Int("gen_ai.prompt.message_count", len(msgs)),
				attribute.Int("gen_ai.request.tool_count", len(ts.tools)),
				attribute.String("gen_ai.input", llm.SerializeMessagesForTrace(msgs)),
				attribute.Bool("gen_ai.stream", true),
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

	if span != nil {
		span.SetAttributes(
			attribute.String("gen_ai.response.finish_reason", finalChunk.FinishReason),
			attribute.Int("gen_ai.response.tool_call_count", len(finalChunk.ToolCalls)),
			attribute.String("gen_ai.output", llm.SerializeResponseForTrace(&llm.Response{
				Text:         fullText,
				Reasoning:    reasoningBuf.String(),
				ToolCalls:    finalChunk.ToolCalls,
				FinishReason: finalChunk.FinishReason,
			})),
		)
		if finalChunk.Usage != nil {
			span.SetAttributes(
				attribute.Int("gen_ai.usage.prompt_tokens", finalChunk.Usage.PromptTokens),
				attribute.Int("gen_ai.usage.completion_tokens", finalChunk.Usage.CompletionTokens),
			)
		}
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
		return a.handleToolLoopFallback(ctx, agentCtx, sess, t, ts, rc, resp, p.Channel, p.LastMessage.ChannelName)
	}

	// ツールなし: コンテキストに追加。
	agentCtx.Add(assistantMessage(fullText, p.Channel, p.LastMessage.ChannelName, nil))

	return a.filterResponse(&llm.Response{Text: fullText, ToolCalls: finalChunk.ToolCalls}, p.Channel), nil
}

// handleToolLoopFallback はストリーミングで ToolCall を検出した後、
// バッチモードの tool loop を実行する。
func (a *Agent) handleToolLoopFallback(
	ctx context.Context,
	agentCtx *Context,
	sess Session,
	t *Thought,
	ts toolSet,
	rc *llm.RoleClient,
	firstResp *llm.Response,
	channel, channelName string,
) (string, error) {
	// skip_response と text を同時生成した場合は tool_calls を剥がして text を採用する。
	if containsSkipTool(firstResp.ToolCalls) {
		execSideEffectsOnSkip(ctx, ts.toolMap, firstResp.ToolCalls, a.logger)
		if firstResp.Text != "" {
			agentCtx.Add(assistantMessage(firstResp.Text, channel, channelName, nil))
			return a.filterResponse(firstResp, channel), nil
		}
		return "", nil
	}

	agentCtx.Add(assistantMessage(firstResp.Text, channel, channelName, firstResp.ToolCalls))

	allStopAfter := a.executeToolCalls(ctx, agentCtx, sess, ts.toolMap, firstResp.ToolCalls, channel, 0)
	if allStopAfter {
		return a.filterResponse(firstResp, channel), nil
	}

	resp, err := a.continueToolLoop(ctx, agentCtx, sess, t, ts, rc, channel, channelName)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	if !resp.HasToolCalls() {
		agentCtx.Add(assistantMessage(resp.Text, channel, channelName, nil))
	}
	return a.filterResponse(resp, channel), nil
}
