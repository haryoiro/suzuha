package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/tool"
	anyllm "github.com/mozilla-ai/any-llm-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	llmerrors "github.com/mozilla-ai/any-llm-go/errors"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
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
	// ImageURLs holds image URLs attached to this message.
	// When the active LLM is vision-capable, these are sent as multimodal content parts.
	ImageURLs []string `json:"image_urls,omitempty"`

	// MediaKeys holds MediaStore keys for persisted media attachments.
	// Used by the consolidator to attach media to extracted memories.
	MediaKeys []string `json:"media_keys,omitempty"`
}

// Response wraps an LLM completion response.
type Response struct {
	Text         string
	Reasoning    string // content inside <think>...</think> tags, if any
	ToolCalls    []providers.ToolCall
	FinishReason string
	Usage        providers.Usage
}

// RawMessage は providers.Message の型エイリアス。
// 外部パッケージが providers を直接 import せずに済むようにする。
type RawMessage = providers.Message

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// parseThinkTags separates reasoning content from response text.
// Handles both "<think>reasoning</think>response" and "reasoning</think>response".
// If the model puts everything inside think tags (cleaned is empty),
// the reasoning content is used as the response text.
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

	// Some models put the entire response inside think tags.
	// Fall back to using reasoning as the response text.
	if cleaned == "" && reasoning != "" {
		cleaned = reasoning
		reasoning = ""
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

// roleProvider はロールに割り当てられたプロバイダの状態。
type roleProvider struct {
	provider     providers.Provider
	providerName string
	model        string
	apiBase      string
	maxCtx       int
	capabilities []string // ["text"], ["text","vision"], etc.
}

// hasCapability はこのプロバイダが指定 capability を持つか返す。
func (rp *roleProvider) hasCapability(cap string) bool {
	for _, c := range rp.capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// Client is a thin wrapper around any-llm-go provider with role-based provider management.
type Client struct {
	mu    sync.RWMutex
	roles map[string]roleProvider // "conversation", "background", "vision", etc.

	// Embedding は Embedder インターフェース経由のため据え置き。
	embeddingProv  providers.Provider
	embeddingModel string
	embeddingDims  int

	logger *slog.Logger
	tracer trace.Tracer // nil when tracing is disabled
}

// SetTracer sets the OpenTelemetry tracer for LLM call tracing.
func (c *Client) SetTracer(t trace.Tracer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tracer = t
}

// ProviderInfo returns the current conversation provider name, model, API base URL, and vision capability.
// 後方互換シム: conversation ロールの情報を返す。
func (c *Client) ProviderInfo() (providerName, model, apiBase string, visionCapable bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rp, ok := c.roles["conversation"]
	if !ok {
		return "", "", "", false
	}
	return rp.providerName, rp.model, rp.apiBase, rp.hasCapability("vision")
}

// SwapRoleSpec はロールのプロバイダを RoleSpec で切り替える。
func (c *Client) SwapRoleSpec(role string, spec RoleSpec) {
	rp := roleProvider{
		provider:     spec.ProviderInst,
		providerName: spec.ProviderName,
		model:        spec.ModelID,
		apiBase:      spec.APIBase,
		maxCtx:       spec.MaxContext,
		capabilities: spec.Capabilities,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roles == nil {
		c.roles = make(map[string]roleProvider)
	}
	c.roles[role] = rp
	c.logger.Info("LLMロールを切り替えた", "role", role, "provider", spec.ProviderName, "model", spec.ModelID, "api_base", spec.APIBase, "max_ctx", spec.MaxContext)
}

// RoleClient はロールに紐づくプロバイダで補完を実行する。
// Client への参照を保持し、呼び出し時に最新の provider を解決する。
// これにより SwapRole() の変更が即座に反映される。
type RoleClient struct {
	client *Client
	role   string // 解決済みのロール名
}

// resolve は呼び出し時に最新の roleProvider を取得する。
func (rc *RoleClient) resolve() roleProvider {
	rc.client.mu.RLock()
	defer rc.client.mu.RUnlock()
	if rp, ok := rc.client.roles[rc.role]; ok {
		return rp
	}
	return roleProvider{}
}

// CompleteRaw はこのロールのプロバイダで completion を実行する。
func (rc *RoleClient) CompleteRaw(ctx context.Context, messages []providers.Message) (*Response, error) {
	rp := rc.resolve()
	if rp.provider == nil {
		return nil, fmt.Errorf("llm: ロール %q にプロバイダが設定されていません", rc.role)
	}
	params := providers.CompletionParams{
		Model:    rp.model,
		Messages: messages,
	}

	var resp *providers.ChatCompletion
	err := retryOnRateLimit(ctx, rc.client.logger, func() error {
		var callErr error
		resp, callErr = rp.provider.Completion(ctx, params)
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

// MaxContextTokens はこのロールの最大コンテキストトークン数を返す。
func (rc *RoleClient) MaxContextTokens() int {
	return rc.resolve().maxCtx
}

// For はロールに割り当てられたプロバイダを返す。
// フォールバック: role → "background" → "conversation"
// 返される RoleClient は Client への参照を保持し、SwapRole() の変更が即座に反映される。
func (c *Client) For(role string) *RoleClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 解決されたロール名を決定
	resolved := role
	fallback := []string{role, "background", "conversation"}
	for _, r := range fallback {
		if _, ok := c.roles[r]; ok {
			resolved = r
			break
		}
	}

	return &RoleClient{client: c, role: resolved}
}

// WithCapability はロールの capability 解決を行い、RoleClient と inline フラグを返す。
// inline=true: ロールのプロバイダがネイティブ対応。
// inline=false: capability 名のロールにフォールバック。
func (c *Client) WithCapability(role, capability string) (*RoleClient, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// ロールのプロバイダを取得
	rp, ok := c.roles[role]
	if !ok {
		// フォールバック
		for _, r := range []string{"background", "conversation"} {
			if rp, ok = c.roles[r]; ok {
				role = r
				break
			}
		}
	}

	// ロールのプロバイダが capability を持つ → inline
	if ok && rp.hasCapability(capability) {
		return &RoleClient{client: c, role: role}, true
	}

	// capability 名のロールにフォールバック
	if _, ok := c.roles[capability]; ok {
		return &RoleClient{client: c, role: capability}, false
	}

	// 見つからない
	return nil, false
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
func NewClient(providerName, model, apiKey, apiBase string, maxCtx int, emb EmbeddingConfig, vis VisionConfig, logger *slog.Logger) (*Client, error) {
	p, err := newProvider(providerName, apiKey, apiBase)
	if err != nil {
		return nil, err
	}

	mainRP := roleProvider{
		provider:     p,
		providerName: providerName,
		model:        model,
		apiBase:      apiBase,
		maxCtx:       maxCtx,
		capabilities: []string{"text"},
	}

	c := &Client{
		roles: map[string]roleProvider{
			"conversation": mainRP,
			"background":   mainRP, // デフォルトは conversation と同じ
		},
		embeddingModel: emb.Model,
		embeddingDims:  emb.Dims,
		logger:         logger,
	}

	// Build embedding provider: use separate provider if configured, otherwise reuse main.
	if emb.Model != "" {
		if emb.Provider != "" && (emb.Provider != providerName || emb.APIKey != apiKey || emb.APIBase != apiBase) {
			ep, err := newProvider(emb.Provider, emb.APIKey, emb.APIBase)
			if err != nil {
				return nil, fmt.Errorf("llm: 埋め込みプロバイダの初期化に失敗: %w", err)
			}
			c.embeddingProv = ep
		} else {
			c.embeddingProv = p
		}
	}

	// Build vision provider: use separate provider if configured.
	if vis.Model != "" {
		visRP := roleProvider{
			providerName: vis.Provider,
			model:        vis.Model,
			apiBase:      vis.APIBase,
			capabilities: []string{"text", "vision"},
		}
		if vis.Provider != "" && (vis.Provider != providerName || vis.APIKey != apiKey || vis.APIBase != apiBase) {
			vp, err := newProvider(vis.Provider, vis.APIKey, vis.APIBase)
			if err != nil {
				return nil, fmt.Errorf("llm: ビジョンプロバイダの初期化に失敗: %w", err)
			}
			visRP.provider = vp
		} else {
			visRP.provider = p
		}
		c.roles["vision"] = visRP
		logger.Info("ビジョンモデルを有効にした", "model", vis.Model)
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
			return nil, fmt.Errorf("llm: プロバイダ %s の初期化に失敗: %w", providerName, err)
		}
		return p, nil
	case "gemini":
		geminiOpts := []anyllm.Option{anyllm.WithAPIKey(apiKey)}
		p, err := gemini.New(geminiOpts...)
		if err != nil {
			return nil, fmt.Errorf("llm: プロバイダ %s の初期化に失敗: %w", providerName, err)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("llm: 未対応のプロバイダ %q", providerName)
	}
}

// MaxContextTokens returns the max context window size for the conversation role.
// 後方互換シム。
func (c *Client) MaxContextTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if rp, ok := c.roles["conversation"]; ok {
		return rp.maxCtx
	}
	return 0
}

// SetMaxContextTokens updates the conversation role's max context window.
func (c *Client) SetMaxContextTokens(maxCtx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rp, ok := c.roles["conversation"]; ok {
		rp.maxCtx = maxCtx
		c.roles["conversation"] = rp
	}
}

// Complete sends a completion request with optional tools.
// 後方互換シム: conversation ロールを使用する。
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
		Messages: convertMessages(messages, vision),
		Tools:    convertTools(tools),
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

// convertMessages transforms suzuha Messages to any-llm-go Messages.
// System messages after the first one are converted to user messages,
// because some models (e.g. Qwen3.5) only allow a single system message at the start.
// Orphaned tool messages and unmatched tool_calls are sanitized to satisfy
// strict providers like OpenAI.
func convertMessages(msgs []Message, visionCapable bool) []providers.Message {
	// Collect tool_call IDs that have assistant requests and tool responses.
	assistantToolCalls := make(map[string]bool)
	toolResponses := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				assistantToolCalls[tc.ID] = true
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			toolResponses[m.ToolCallID] = true
		}
	}

	out := make([]providers.Message, 0, len(msgs))
	seenSystem := false

	for _, m := range msgs {
		role := m.Role
		content := m.Content

		// Drop orphaned tool responses (no matching assistant tool_calls).
		if role == "tool" && m.ToolCallID != "" && !assistantToolCalls[m.ToolCallID] {
			continue
		}

		if role == "system" {
			if seenSystem {
				role = "user"
				content = "[system]\n" + content
			}
			seenSystem = true
		}
		// Embed message metadata so the LLM can identify channel context.
		if m.Role == "user" && m.MessageID != "" {
			ts := ""
			if !m.Timestamp.IsZero() {
				ts = m.Timestamp.Format("2006-01-02 15:04")
			}
			content = fmt.Sprintf("[time=%s server=%s channel=#%s channel_id=%s guild_id=%s message_id=%s platform=%s user_id=%s user=%s]\n%s",
				ts, m.GuildName, m.ChannelName, m.Channel, m.GuildID, m.MessageID, m.Source, m.UserID, m.UserName, m.Content)
		}
		// assistant メッセージには channel 名だけを最小限付与。
		// フルメタデータを付けると LLM がフォーマットを真似るため、チャンネル名のみ。
		if m.Role == "assistant" && m.Channel != "" && m.ChannelName != "" {
			content = fmt.Sprintf("[channel=#%s]\n%s", m.ChannelName, content)
		}

		// Strip tool_calls from assistant messages if any response is missing.
		var toolCalls []providers.ToolCall
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			allPresent := true
			for _, tc := range m.ToolCalls {
				if !toolResponses[tc.ID] {
					allPresent = false
					break
				}
			}
			if allPresent {
				toolCalls = m.ToolCalls
			}
			// else: drop tool_calls entirely — responses are missing
		} else {
			toolCalls = m.ToolCalls
		}

		// Build multimodal content if the LLM supports vision and there are images.
		var msgContent any = content
		if visionCapable && len(m.ImageURLs) > 0 && role == "user" {
			parts := []providers.ContentPart{
				{Type: "text", Text: content},
			}
			for _, u := range m.ImageURLs {
				parts = append(parts, providers.ContentPart{
					Type:     "image_url",
					ImageURL: &providers.ImageURL{URL: u},
				})
			}
			msgContent = parts
		}

		out = append(out, providers.Message{
			Role:       role,
			Content:    msgContent,
			ToolCalls:  toolCalls,
			ToolCallID: m.ToolCallID,
		})
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
		return nil, fmt.Errorf("llm: 埋め込みプロバイダ %q は埋め込みをサポートしていません", c.embeddingProv.Name())
	}

	params := providers.EmbeddingParams{
		Model: c.embeddingModel,
		Input: text,
	}
	if c.embeddingDims > 0 {
		dims := c.embeddingDims
		params.Dimensions = &dims
	}

	c.logger.Debug("埋め込みリクエスト", "model", c.embeddingModel, "text_length", len(text))

	var resp *providers.EmbeddingResponse
	start := time.Now()

	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = ep.Embedding(ctx, params)
		return callErr
	})
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("埋め込みに失敗しました", "model", c.embeddingModel, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return nil, fmt.Errorf("llm: 埋め込みに失敗: %w", err)
	}

	if len(resp.Data) == 0 {
		c.logger.Warn("埋め込みの空レスポンスを受信しました", "model", c.embeddingModel)
		return nil, fmt.Errorf("llm: 埋め込みの空レスポンス")
	}

	// Convert float64 (API response) to float32 (sqlite-vec storage).
	f64 := resp.Data[0].Embedding
	result := make([]float32, len(f64))
	for i, v := range f64 {
		result[i] = float32(v)
	}

	c.logger.Debug("埋め込み完了", "model", c.embeddingModel, "dims", len(result), "elapsed_ms", elapsed.Milliseconds())
	return result, nil
}

// HasVisionCapability returns whether vision is available and whether it's inline.
// Satisfies vision.VisionDescriber interface.
func (c *Client) HasVisionCapability() (available bool, inline bool) {
	rc, inl := c.WithCapability("conversation", "vision")
	return rc != nil, inl
}

// HasVision returns true if vision is available.
// 後方互換シム。
func (c *Client) HasVision() bool {
	avail, _ := c.HasVisionCapability()
	return avail
}

// IsVisionCapable returns true if the active conversation LLM provider supports vision natively.
// 後方互換シム。
func (c *Client) IsVisionCapable() bool {
	_, inline := c.HasVisionCapability()
	return inline
}

// DescribeImage sends an image URL to a vision model and returns a text description.
// 後方互換シム: WithCapability("conversation", "vision") を使用する。
func (c *Client) DescribeImage(ctx context.Context, imageURL string, prompt ...string) (string, error) {
	rc, _ := c.WithCapability("conversation", "vision")
	if rc == nil {
		return "", fmt.Errorf("llm: ビジョンモデルが設定されていません")
	}
	rp := rc.resolve()
	if rp.provider == nil {
		return "", fmt.Errorf("llm: ビジョンモデルが設定されていません")
	}
	prov := rp.provider
	model := rp.model

	textPrompt := "この画像の内容を簡潔に描写してください。"
	if len(prompt) > 0 && prompt[0] != "" {
		textPrompt = prompt[0]
	}

	params := providers.CompletionParams{
		Model: model,
		Messages: []providers.Message{
			{
				Role: "user",
				Content: []providers.ContentPart{
					{Type: "text", Text: textPrompt},
					{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL}},
				},
			},
		},
	}

	c.logger.Debug("ビジョンリクエスト", "model", model, "url", imageURL)

	var resp *providers.ChatCompletion
	start := time.Now()

	err := retryOnRateLimit(ctx, c.logger, func() error {
		var callErr error
		resp, callErr = prov.Completion(ctx, params)
		return callErr
	})
	elapsed := time.Since(start)

	if err != nil {
		c.logger.Error("ビジョン補完に失敗しました", "model", model, "elapsed_ms", elapsed.Milliseconds(), "error", err)
		return "", fmt.Errorf("llm: ビジョンに失敗: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("llm: ビジョン: 空のレスポンス")
	}

	text := resp.Choices[0].Message.ContentString()
	c.logger.Info("ビジョン補完完了", "model", model, "elapsed_ms", elapsed.Milliseconds(), "description_length", len(text))
	return text, nil
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
