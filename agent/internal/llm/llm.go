package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
	"go.opentelemetry.io/otel/trace"
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

// CompleteWithTools はツール定義付きで completion を実行する。
// メッセージとツールは事前に providers 形式に変換済みであること。
func (rc *RoleClient) CompleteWithTools(ctx context.Context, messages []providers.Message, tools []providers.Tool) (*Response, error) {
	rp := rc.resolve()
	if rp.provider == nil {
		return nil, fmt.Errorf("llm: ロール %q にプロバイダが設定されていません", rc.role)
	}
	params := providers.CompletionParams{
		Model:    rp.model,
		Messages: messages,
		Tools:    tools,
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
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}
	return r, nil
}

// HasCapability はこのロールが指定されたケイパビリティを持つかを返す。
func (rc *RoleClient) HasCapability(cap string) bool {
	rc.client.mu.RLock()
	defer rc.client.mu.RUnlock()
	if rp, ok := rc.client.roles[rc.role]; ok {
		return rp.hasCapability(cap)
	}
	return false
}

// Model はこのロールに割り当てられたモデル名を返す。
func (rc *RoleClient) Model() string {
	return rc.resolve().model
}

// ProviderName はこのロールに割り当てられたプロバイダ名を返す。
func (rc *RoleClient) ProviderName() string {
	return rc.resolve().providerName
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
