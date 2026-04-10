package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/tool"
	llmerrors "github.com/mozilla-ai/any-llm-go/errors"
	"github.com/mozilla-ai/any-llm-go/providers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Complete sends a completion request with optional tools.
// 後方互換シム: conversation ロールを使用する。
// Deprecated: Complete はメッセージ/ツール変換と tracing を内包している。
// 新規コードは ConvertMessages/ConvertTools + RoleClient.CompleteWithTools を使用すること。
func (c *Client) Complete(ctx context.Context, messages []Message, tools []tool.Tool) (*Response, error) {
	c.mu.RLock()
	rp := c.roles["conversation"]
	prov := rp.provider
	provName := rp.providerName
	model := rp.model
	vision := rp.hasCapability("vision")
	tracer := c.tracer
	c.mu.RUnlock()

	// Start LLM generation span if tracer is available.
	var span trace.Span
	if tracer != nil {
		// Serialize messages for tracing (role + content only, skip images).
		inputJSON := serializeMessagesForTrace(messages)

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
		Messages: ConvertMessages(messages, vision),
		Tools:    ConvertTools(tools),
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

	// Record LLM response attributes to span.
	if span != nil {
		outputJSON := serializeResponseForTrace(r)
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

// CompleteRaw sends a completion request with pre-built provider messages (no tool support).
// 後方互換シム: conversation ロールを使用する。
func (c *Client) CompleteRaw(ctx context.Context, messages []providers.Message) (*Response, error) {
	c.mu.RLock()
	rp := c.roles["conversation"]
	c.mu.RUnlock()
	return c.completeRaw(ctx, rp.provider, rp.model, messages)
}

// CompleteRawDefault sends a completion request using the background provider.
// 後方互換シム: background ロールを使用する。
func (c *Client) CompleteRawDefault(ctx context.Context, messages []providers.Message) (*Response, error) {
	c.mu.RLock()
	rp, ok := c.roles["background"]
	if !ok {
		rp = c.roles["conversation"]
	}
	c.mu.RUnlock()
	return c.completeRaw(ctx, rp.provider, rp.model, messages)
}

func (c *Client) completeRaw(ctx context.Context, prov providers.Provider, model string, messages []providers.Message) (*Response, error) {
	params := providers.CompletionParams{
		Model:    model,
		Messages: messages,
	}

	var resp *providers.ChatCompletion
	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = prov.Completion(ctx, params)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("llm: 補完に失敗: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: 空のレスポンス")
	}

	choice := resp.Choices[0]
	reasoning, cleaned := parseThinkTags(choice.Message.ContentString())
	r := &Response{
		Text:         cleaned,
		Reasoning:    reasoning,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}
	return r, nil
}

// retryOnRateLimit retries fn with exponential backoff when a rate-limit error is returned.
// maxRetries=3, base delay=1s → waits 1s, 2s, 4s.
func retryOnRateLimit(ctx context.Context, logger *slog.Logger, fn func() error) error {
	const maxRetries = 3
	const baseDelay = time.Second

	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err = fn()
		if err == nil || !errors.Is(err, llmerrors.ErrRateLimit) {
			return err
		}
		if attempt == maxRetries {
			break
		}
		delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
		logger.Warn("レートリミット、リトライします", "attempt", attempt+1, "delay", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

// serializeMessagesForTrace converts messages to a JSON array for Langfuse input.
// Images are excluded to keep the payload manageable.
func serializeMessagesForTrace(messages []Message) string {
	type traceMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Name    string `json:"name,omitempty"`
	}
	out := make([]traceMsg, 0, len(messages))
	for _, m := range messages {
		name := m.UserName
		if m.Role == "tool" {
			name = m.ToolCallID
		}
		out = append(out, traceMsg{
			Role:    m.Role,
			Content: m.Content,
			Name:    name,
		})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// serializeResponseForTrace converts a Response to JSON for Langfuse output.
func serializeResponseForTrace(r *Response) string {
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
