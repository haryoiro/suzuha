package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/llmconv"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/port/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Complete sends a completion request with optional tools.
// 後方互換シム: conversation ロールを使用する。
// Deprecated: Complete はメッセージ/ツール変換と tracing を内包している。
// 新規コードは ConvertMessages/ConvertTools + RoleClient.CompleteWithTools を使用すること。
func (c *Client) Complete(ctx context.Context, messages []message.Message, tools []tool.Tool) (*Response, error) {
	c.mu.RLock()
	rp := c.roles["conversation"]
	prov := rp.provider
	provName := rp.providerName
	model := rp.model
	vision := rp.hasCapability("vision")
	tracer := c.tracer
	c.mu.RUnlock()

	var span trace.Span
	if tracer != nil {
		inputJSON := langfuse.SerializeMessages(messages)

		ctx, span = tracer.Start(ctx, "llm.complete",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("gen_ai.system", provName),
				attribute.String("gen_ai.request.model", model),
				attribute.Int("gen_ai.prompt.message_count", len(messages)),
				attribute.Int("gen_ai.request.tool_count", len(tools)),
				attribute.String("gen_ai.input", inputJSON),
			),
		)
		defer span.End()
	}

	params := providers.CompletionParams{
		Model:    model,
		Messages: llmconv.ConvertMessages(messages, vision),
		Tools:    llmconv.ConvertTools(tools),
	}

	c.logger.Debug("LLMにリクエスト",
		"model", model,
		"messages", len(messages),
		"tools", len(tools))

	var resp *providers.ChatCompletion
	start := time.Now()

	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = prov.Completion(ctx, params)
		return callErr
	})
	elapsed := time.Since(start)

	if err != nil {
		if span != nil {
			span.RecordError(err)
		}
		c.logger.Error("LLMが答えてくれなかった", "model", model, "elapsed_ms", elapsed.Milliseconds(), "error", err.Error())
		return nil, fmt.Errorf("llm: 補完に失敗: %w", err)
	}

	if len(resp.Choices) == 0 {
		c.logger.Warn("LLMが何も言わなかった", "model", model, "elapsed_ms", elapsed.Milliseconds())
		return nil, fmt.Errorf("llm: 空のレスポンス")
	}

	choice := resp.Choices[0]
	reasoning, cleaned := parseThinkTags(choice.Message.ContentString())
	r := &Response{
		Text:         cleaned,
		Reasoning:    reasoning,
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}

	if span != nil {
		outputJSON := langfuse.SerializeResponse(r)
		span.SetAttributes(
			attribute.Int("gen_ai.usage.prompt_tokens", r.Usage.PromptTokens),
			attribute.Int("gen_ai.usage.completion_tokens", r.Usage.CompletionTokens),
			attribute.String("gen_ai.response.finish_reason", r.FinishReason),
			attribute.Int("gen_ai.response.tool_call_count", len(r.ToolCalls)),
			attribute.Int64("gen_ai.response.latency_ms", elapsed.Milliseconds()),
			attribute.String("gen_ai.output", outputJSON),
		)
	}

	c.logger.Info("LLMが答えた",
		"model", model,
		"elapsed_ms", elapsed.Milliseconds(),
		"finish_reason", r.FinishReason,
		"tokens_in", r.Usage.PromptTokens,
		"tokens_out", r.Usage.CompletionTokens,
		"tool_calls", len(r.ToolCalls))
	if reasoning != "" {
		c.logger.Debug("llm 推論内容", "length", len(reasoning),
			"content", textutil.TruncateRunes(reasoning, 300))
	}

	return r, nil
}
