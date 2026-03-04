package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	Reasoning    string // content inside <think>...</think> tags, if any
	ToolCalls    []providers.ToolCall
	FinishReason string
	Usage        providers.Usage
}

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// parseThinkTags separates reasoning content from response text.
// Handles both "<think>reasoning</think>response" and "reasoning</think>response".
func parseThinkTags(text string) (reasoning, cleaned string) {
	const closeTag = "</think>"
	idx := strings.LastIndex(text, closeTag)
	if idx < 0 {
		return "", text
	}

	raw := text[:idx]
	cleaned = strings.TrimSpace(text[idx+len(closeTag):])

	const openTag = "<think>"
	if oi := strings.Index(raw, openTag); oi >= 0 {
		reasoning = strings.TrimSpace(raw[oi+len(openTag):])
	} else {
		reasoning = strings.TrimSpace(raw)
	}
	return reasoning, cleaned
}

// directiveTags are agent-internal tags used to control response behavior.
// They must never appear in user-visible output.
var directiveTags = []string{"[RESPOND]", "[LISTEN]", "[SKIP]"}

// StripDirectiveTags removes agent directive tags ([RESPOND], [LISTEN], [SKIP])
// from LLM output. Use this before sending any LLM text to chat.
func StripDirectiveTags(text string) string {
	for _, tag := range directiveTags {
		text = strings.ReplaceAll(text, tag, "")
	}
	return strings.TrimSpace(text)
}

// IsSilentResponse returns true if the LLM chose not to respond
// (empty text or contains [SKIP]).
func IsSilentResponse(text string) bool {
	return text == "" || strings.Contains(strings.ToUpper(text), "[SKIP]")
}

// Client is a thin wrapper around any-llm-go provider.
type Client struct {
	mu              sync.RWMutex
	provider        providers.Provider
	providerName    string
	model           string
	apiBase         string
	// defaultProvider is the original provider from config — used for tasks
	// like compaction that should always use the large model.
	defaultProvider providers.Provider
	defaultModel    string
	embeddingProv   providers.Provider // may differ from provider (e.g. OpenAI for embeddings)
	embeddingModel  string
	embeddingDims   int
	visionProv      providers.Provider
	visionModel     string
	visionCapable   bool // true if active provider supports vision natively
	maxCtx          int
	metrics         *observe.Metrics
	logger          *slog.Logger
}

// ProviderInfo returns the current provider name, model, API base URL, and vision capability.
func (c *Client) ProviderInfo() (providerName, model, apiBase string, visionCapable bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providerName, c.model, c.apiBase, c.visionCapable
}

// SwapProvider atomically replaces the completion provider and model.
// If maxCtx > 0, the max context window is also updated.
// Embedding and vision providers are not affected.
func (c *Client) SwapProvider(providerName, model, apiKey, apiBase string, maxCtx int, visionCapable bool) error {
	p, err := newProvider(providerName, apiKey, apiBase)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.provider = p
	c.providerName = providerName
	c.model = model
	c.apiBase = apiBase
	c.visionCapable = visionCapable
	if maxCtx > 0 {
		c.maxCtx = maxCtx
	}
	c.logger.Info("llm provider swapped", "provider", providerName, "model", model, "api_base", apiBase, "max_ctx", c.maxCtx, "vision", visionCapable)
	return nil
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
		provider:        p,
		providerName:    providerName,
		model:           model,
		apiBase:         apiBase,
		defaultProvider: p,
		defaultModel:    model,
		embeddingModel:  emb.Model,
		embeddingDims:   emb.Dims,
		maxCtx:          maxCtx,
		metrics:         metrics,
		logger:          logger,
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
	case "openai", "zhipu", "qwen":
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
	c.mu.RLock()
	prov := c.provider
	model := c.model
	c.mu.RUnlock()

	params := providers.CompletionParams{
		Model:    model,
		Messages: convertMessages(messages),
		Tools:    convertTools(tools),
	}

	c.logger.Debug("llm request",
		"model", model,
		"messages", len(messages),
		"tools", len(tools))

	start := time.Now()
	resp, err := prov.Completion(ctx, params)
	elapsed := time.Since(start)

	if c.metrics != nil {
		c.metrics.LLMLatency.Observe(elapsed.Seconds())
	}

	if err != nil {
		c.logger.Error("llm completion failed", "model", model, "elapsed_ms", elapsed.Milliseconds(), "error", err.Error())
		return nil, fmt.Errorf("llm: completion: %w", err)
	}

	if c.metrics != nil && resp.Usage != nil {
		c.metrics.LLMTokensIn.Add(float64(resp.Usage.PromptTokens))
		c.metrics.LLMTokensOut.Add(float64(resp.Usage.CompletionTokens))
	}

	if len(resp.Choices) == 0 {
		c.logger.Warn("llm empty response", "model", model, "elapsed_ms", elapsed.Milliseconds())
		return nil, fmt.Errorf("llm: empty response")
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

	c.logger.Info("llm completion",
		"model", model,
		"elapsed_ms", elapsed.Milliseconds(),
		"finish_reason", r.FinishReason,
		"tokens_in", r.Usage.PromptTokens,
		"tokens_out", r.Usage.CompletionTokens,
		"tool_calls", len(r.ToolCalls))
	if reasoning != "" {
		c.logger.Debug("llm reasoning", "length", len(reasoning),
			"content", truncateStr(reasoning, 300))
	}

	return r, nil
}

// CompleteRaw sends a completion request with pre-built provider messages (no tool support).
// Uses the currently active provider (which may have been swapped at runtime).
func (c *Client) CompleteRaw(ctx context.Context, messages []providers.Message) (*Response, error) {
	c.mu.RLock()
	prov := c.provider
	model := c.model
	c.mu.RUnlock()
	return c.completeRaw(ctx, prov, model, messages)
}

// CompleteRawDefault sends a completion request using the default (config) provider,
// regardless of any runtime provider swap. Use this for background tasks like
// compaction that should always use the large model.
func (c *Client) CompleteRawDefault(ctx context.Context, messages []providers.Message) (*Response, error) {
	return c.completeRaw(ctx, c.defaultProvider, c.defaultModel, messages)
}

func (c *Client) completeRaw(ctx context.Context, prov providers.Provider, model string, messages []providers.Message) (*Response, error) {
	params := providers.CompletionParams{
		Model:    model,
		Messages: messages,
	}

	resp, err := prov.Completion(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("llm: completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: empty response")
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

// convertMessages transforms suzuha Messages to any-llm-go Messages.
// System messages after the first one are converted to user messages,
// because some models (e.g. Qwen3.5) only allow a single system message at the start.
func convertMessages(msgs []Message) []providers.Message {
	out := make([]providers.Message, len(msgs))
	seenSystem := false
	for i, m := range msgs {
		role := m.Role
		content := m.Content
		if role == "system" {
			if seenSystem {
				role = "user"
				content = "[system]\n" + content
			}
			seenSystem = true
		}
		// Embed message metadata so the LLM can identify channel context.
		// Only tag user messages — tagging assistant messages causes the LLM
		// to mimic the format and include channel IDs in its responses.
		if m.Role == "user" && m.MessageID != "" {
			ts := ""
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format("2006-01-02 15:04")
			}
			content = fmt.Sprintf("[time=%s server=%s channel=#%s channel_id=%s guild_id=%s message_id=%s platform=%s user_id=%s user=%s]\n%s",
				ts, m.GuildName, m.ChannelName, m.Channel, m.GuildID, m.MessageID, m.Source, m.UserID, m.UserName, m.Content)
		}
		out[i] = providers.Message{
			Role:       role,
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

// HasVision returns true if vision is available (either via dedicated provider or active VLM).
func (c *Client) HasVision() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.visionCapable {
		return true
	}
	return c.visionModel != "" && c.visionProv != nil
}

// DescribeImage sends an image URL to a vision model and returns a text description.
// If the active provider is vision-capable, it is used directly; otherwise falls back
// to the dedicated vision provider from config.
func (c *Client) DescribeImage(ctx context.Context, imageURL string) (string, error) {
	c.mu.RLock()
	prov, model := c.visionProv, c.visionModel
	if c.visionCapable {
		prov = c.provider
		model = c.model
	}
	c.mu.RUnlock()

	if prov == nil {
		return "", fmt.Errorf("llm: vision model not configured")
	}

	params := providers.CompletionParams{
		Model: model,
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

	c.logger.Debug("vision request", "model", model, "url", imageURL)

	start := time.Now()
	resp, err := prov.Completion(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("vision completion failed", "model", model, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return "", fmt.Errorf("llm: vision: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: vision: empty response")
	}

	text := resp.Choices[0].Message.ContentString()
	c.logger.Info("vision completion", "model", model, "elapsed_ms", elapsed.Milliseconds(), "description_length", len(text))
	return text, nil
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
