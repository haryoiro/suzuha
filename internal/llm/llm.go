package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
)

// Message is suzuha's internal message format with channel/user context.
type Message struct {
	Role       string    `json:"role"` // "user", "assistant", "system", "tool"
	Content    string    `json:"content"`
	UserID     string    `json:"user_id,omitempty"`
	UserName   string    `json:"user_name,omitempty"`
	Source     string    `json:"source,omitempty"` // "discord", "cli"
	Channel    string    `json:"channel,omitempty"`
	MessageID  string    `json:"message_id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	ToolCallID string    `json:"tool_call_id,omitempty"`

	// ToolCalls is set when the assistant wants to invoke tools.
	ToolCalls []providers.ToolCall `json:"tool_calls,omitempty"`
}

// Response wraps an LLM completion response.
type Response struct {
	Text         string
	ToolCalls    []providers.ToolCall
	FinishReason string
	Usage        providers.Usage
}

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// Client is a thin wrapper around any-llm-go provider.
type Client struct {
	provider       providers.Provider
	model          string
	embeddingModel string
	embeddingDims  int
	maxCtx         int
	metrics        *observe.Metrics
	logger         *slog.Logger
}

// NewClient creates a new LLM client.
// apiBase is optional; if empty, the provider's default base URL is used.
// embeddingModel is optional; if empty, Embed() returns nil.
func NewClient(providerName, model, apiKey, apiBase string, maxCtx int, embeddingModel string, embeddingDims int, metrics *observe.Metrics, logger *slog.Logger) (*Client, error) {
	var p providers.Provider
	var err error

	opts := []anyllm.Option{anyllm.WithAPIKey(apiKey)}
	if apiBase != "" {
		opts = append(opts, anyllm.WithBaseURL(apiBase))
	}

	switch providerName {
	case "openai", "zhipu":
		// ZhiPu and other OpenAI-compatible providers use the same client with a custom base URL.
		p, err = openai.New(opts...)
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", providerName)
	}
	if err != nil {
		return nil, fmt.Errorf("llm: init provider %s: %w", providerName, err)
	}

	return &Client{
		provider:       p,
		model:          model,
		embeddingModel: embeddingModel,
		embeddingDims:  embeddingDims,
		maxCtx:         maxCtx,
		metrics:        metrics,
		logger:         logger,
	}, nil
}

// MaxContextTokens returns the max context window size.
func (c *Client) MaxContextTokens() int {
	return c.maxCtx
}

// Complete sends a completion request with optional tools.
func (c *Client) Complete(ctx context.Context, messages []Message, tools []tool.Tool) (*Response, error) {
	params := providers.CompletionParams{
		Model:    c.model,
		Messages: convertMessages(messages),
		Tools:    convertTools(tools),
	}

	c.logger.Debug("llm request",
		"model", c.model,
		"messages", len(messages),
		"tools", len(tools))

	start := time.Now()
	resp, err := c.provider.Completion(ctx, params)
	elapsed := time.Since(start)

	if c.metrics != nil {
		c.metrics.LLMLatency.Observe(elapsed.Seconds())
	}

	if err != nil {
		c.logger.Error("llm completion failed", "model", c.model, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return nil, fmt.Errorf("llm: completion: %w", err)
	}

	if c.metrics != nil && resp.Usage != nil {
		c.metrics.LLMTokensIn.Add(float64(resp.Usage.PromptTokens))
		c.metrics.LLMTokensOut.Add(float64(resp.Usage.CompletionTokens))
	}

	if len(resp.Choices) == 0 {
		c.logger.Warn("llm empty response", "model", c.model, "elapsed_ms", elapsed.Milliseconds())
		return nil, fmt.Errorf("llm: empty response")
	}

	choice := resp.Choices[0]
	r := &Response{
		Text:         choice.Message.ContentString(),
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}

	c.logger.Info("llm completion",
		"model", c.model,
		"elapsed_ms", elapsed.Milliseconds(),
		"finish_reason", r.FinishReason,
		"tokens_in", r.Usage.PromptTokens,
		"tokens_out", r.Usage.CompletionTokens,
		"tool_calls", len(r.ToolCalls))

	return r, nil
}

// CompleteRaw sends a completion request with pre-built provider messages (no tool support).
func (c *Client) CompleteRaw(ctx context.Context, messages []providers.Message) (*Response, error) {
	params := providers.CompletionParams{
		Model:    c.model,
		Messages: messages,
	}

	resp, err := c.provider.Completion(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("llm: completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: empty response")
	}

	choice := resp.Choices[0]
	r := &Response{
		Text:         choice.Message.ContentString(),
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}
	return r, nil
}

// convertMessages transforms suzuha Messages to any-llm-go Messages.
func convertMessages(msgs []Message) []providers.Message {
	out := make([]providers.Message, len(msgs))
	for i, m := range msgs {
		content := m.Content
		// Embed message metadata so the LLM can identify channel context.
		// Only tag user messages — tagging assistant messages causes the LLM
		// to mimic the format and include channel IDs in its responses.
		if m.Role == "user" && m.MessageID != "" {
			content = fmt.Sprintf("[channel_id=%s message_id=%s platform=%s user_id=%s user=%s]\n%s",
				m.Channel, m.MessageID, m.Source, m.UserID, m.UserName, m.Content)
		}
		out[i] = providers.Message{
			Role:       m.Role,
			Content:    content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}
	}
	return out
}

// convertTools transforms suzuha Tool interfaces to any-llm-go Tool structs.
func convertTools(tools []tool.Tool) []providers.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]providers.Tool, len(tools))
	for i, t := range tools {
		var params map[string]any
		_ = json.Unmarshal(t.InputSchema(), &params)
		out[i] = providers.Tool{
			Type: "function",
			Function: providers.Function{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		}
	}
	return out
}

// Embed generates an embedding vector for the given text.
// Returns nil, nil if no embedding model is configured.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.embeddingModel == "" {
		return nil, nil
	}

	ep, ok := c.provider.(providers.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("llm: provider %q does not support embeddings", c.provider.Name())
	}

	params := providers.EmbeddingParams{
		Model: c.embeddingModel,
		Input: text,
	}
	if c.embeddingDims > 0 {
		dims := c.embeddingDims
		params.Dimensions = &dims
	}

	c.logger.Debug("embedding request", "model", c.embeddingModel, "text_length", len(text))

	start := time.Now()
	resp, err := ep.Embedding(ctx, params)
	elapsed := time.Since(start)

	if c.metrics != nil {
		c.metrics.EmbeddingLatency.Observe(elapsed.Seconds())
	}

	if err != nil {
		c.logger.Error("embedding failed", "model", c.embeddingModel, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return nil, fmt.Errorf("llm: embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		c.logger.Warn("empty embedding response", "model", c.embeddingModel)
		return nil, fmt.Errorf("llm: empty embedding response")
	}

	// Convert float64 (API response) to float32 (sqlite-vec storage).
	f64 := resp.Data[0].Embedding
	result := make([]float32, len(f64))
	for i, v := range f64 {
		result[i] = float32(v)
	}

	c.logger.Debug("embedding done", "model", c.embeddingModel, "dims", len(result), "elapsed_ms", elapsed.Milliseconds())
	return result, nil
}
