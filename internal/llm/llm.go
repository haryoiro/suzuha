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
	Channel    string    `json:"channel,omitempty"`
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
	provider providers.Provider
	model    string
	maxCtx   int
	metrics  *observe.Metrics
	logger   *slog.Logger
}

// NewClient creates a new LLM client.
func NewClient(providerName, model, apiKey string, maxCtx int, metrics *observe.Metrics, logger *slog.Logger) (*Client, error) {
	var p providers.Provider
	var err error

	switch providerName {
	case "openai":
		p, err = openai.New(anyllm.WithAPIKey(apiKey))
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", providerName)
	}
	if err != nil {
		return nil, fmt.Errorf("llm: init provider %s: %w", providerName, err)
	}

	return &Client{
		provider: p,
		model:    model,
		maxCtx:   maxCtx,
		metrics:  metrics,
		logger:   logger,
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

	start := time.Now()
	resp, err := c.provider.Completion(ctx, params)
	elapsed := time.Since(start)

	if c.metrics != nil {
		c.metrics.LLMLatency.Observe(elapsed.Seconds())
	}

	if err != nil {
		return nil, fmt.Errorf("llm: completion: %w", err)
	}

	if c.metrics != nil && resp.Usage != nil {
		c.metrics.LLMTokensIn.Add(float64(resp.Usage.PromptTokens))
		c.metrics.LLMTokensOut.Add(float64(resp.Usage.CompletionTokens))
	}

	if len(resp.Choices) == 0 {
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
		if m.Role == "user" && m.Channel != "" {
			content = fmt.Sprintf("[%s in #%s]: %s", m.UserName, m.Channel, m.Content)
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
