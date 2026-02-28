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
	Source      string    `json:"source,omitempty"` // "discord", "cli"
	Channel     string    `json:"channel,omitempty"`
	ChannelName string    `json:"channel_name,omitempty"`
	GuildID     string    `json:"guild_id,omitempty"`
	GuildName   string    `json:"guild_name,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
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
	provider        providers.Provider
	model           string
	embeddingProv   providers.Provider // may differ from provider (e.g. OpenAI for embeddings)
	embeddingModel  string
	embeddingDims   int
	visionProv      providers.Provider
	visionModel     string
	maxCtx          int
	metrics         *observe.Metrics
	logger          *slog.Logger
}

// EmbeddingConfig holds optional embedding provider settings.
// If Provider is empty, the main LLM provider is reused.
type EmbeddingConfig struct {
	Provider string
	Model    string
	APIKey   string
	APIBase  string
	Dims     int
}

// VisionConfig holds optional vision model settings.
// If Provider is empty, vision is disabled.
type VisionConfig struct {
	Provider string
	Model    string
	APIKey   string
	APIBase  string
}

// NewClient creates a new LLM client.
func NewClient(providerName, model, apiKey, apiBase string, maxCtx int, emb EmbeddingConfig, vis VisionConfig, metrics *observe.Metrics, logger *slog.Logger) (*Client, error) {
	p, err := newProvider(providerName, apiKey, apiBase)
	if err != nil {
		return nil, err
	}

	c := &Client{
		provider:       p,
		model:          model,
		embeddingModel: emb.Model,
		embeddingDims:  emb.Dims,
		maxCtx:         maxCtx,
		metrics:        metrics,
		logger:         logger,
	}

	// Build embedding provider: use separate provider if configured, otherwise reuse main.
	if emb.Model != "" {
		if emb.Provider != "" && (emb.Provider != providerName || emb.APIKey != apiKey || emb.APIBase != apiBase) {
			ep, err := newProvider(emb.Provider, emb.APIKey, emb.APIBase)
			if err != nil {
				return nil, fmt.Errorf("llm: init embedding provider: %w", err)
			}
			c.embeddingProv = ep
		} else {
			c.embeddingProv = p
		}
	}

	// Build vision provider: use separate provider if configured.
	if vis.Model != "" {
		c.visionModel = vis.Model
		if vis.Provider != "" && (vis.Provider != providerName || vis.APIKey != apiKey || vis.APIBase != apiBase) {
			vp, err := newProvider(vis.Provider, vis.APIKey, vis.APIBase)
			if err != nil {
				return nil, fmt.Errorf("llm: init vision provider: %w", err)
			}
			c.visionProv = vp
		} else {
			c.visionProv = p
		}
		logger.Info("vision model enabled", "model", vis.Model)
	}

	return c, nil
}

func newProvider(providerName, apiKey, apiBase string) (providers.Provider, error) {
	opts := []anyllm.Option{anyllm.WithAPIKey(apiKey)}
	if apiBase != "" {
		opts = append(opts, anyllm.WithBaseURL(apiBase))
	}
	switch providerName {
	case "openai", "zhipu":
		p, err := openai.New(opts...)
		if err != nil {
			return nil, fmt.Errorf("llm: init provider %s: %w", providerName, err)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", providerName)
	}
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
			content = fmt.Sprintf("[server=%s channel=#%s channel_id=%s guild_id=%s message_id=%s platform=%s user_id=%s user=%s]\n%s",
				m.GuildName, m.ChannelName, m.Channel, m.GuildID, m.MessageID, m.Source, m.UserID, m.UserName, m.Content)
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
	if c.embeddingModel == "" || c.embeddingProv == nil {
		return nil, nil
	}

	ep, ok := c.embeddingProv.(providers.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("llm: embedding provider %q does not support embeddings", c.embeddingProv.Name())
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

// HasVision returns true if a vision model is configured.
func (c *Client) HasVision() bool {
	return c.visionModel != "" && c.visionProv != nil
}

// DescribeImage sends an image URL to the vision model and returns a text description.
func (c *Client) DescribeImage(ctx context.Context, imageURL string) (string, error) {
	if !c.HasVision() {
		return "", fmt.Errorf("llm: vision model not configured")
	}

	params := providers.CompletionParams{
		Model: c.visionModel,
		Messages: []providers.Message{
			{
				Role: "user",
				Content: []providers.ContentPart{
					{Type: "text", Text: "この画像の内容を簡潔に描写してください。"},
					{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL}},
				},
			},
		},
	}

	c.logger.Debug("vision request", "model", c.visionModel, "url", imageURL)

	start := time.Now()
	resp, err := c.visionProv.Completion(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("vision completion failed", "model", c.visionModel, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return "", fmt.Errorf("llm: vision: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: vision: empty response")
	}

	text := resp.Choices[0].Message.ContentString()
	c.logger.Info("vision completion", "model", c.visionModel, "elapsed_ms", elapsed.Milliseconds(), "description_length", len(text))
	return text, nil
}
