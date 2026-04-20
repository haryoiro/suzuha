// Package llm は role-based なプロバイダ管理と補完実行を提供する capability。
//
// Client は複数ロール (conversation / background / vision / embedding) の
// プロバイダを保持し、role swap により runtime を停止せずに切り替え可能。
//
// ファイル分割:
//   - llm.go         — Client / roleProvider / NewClient / 基本メソッド + 共有 helpers (parseThinkTags / retryOnRateLimit)
//   - role_client.go — RoleClient と role 解決 (For / WithCapability / fallback)
//   - complete.go    — Client.Complete (後方互換シム、domain→SDK 変換 + tracing)
//   - embed.go       — Client.Embed
//   - vision.go      — DescribeImage / HasVisionCapability
//   - counter.go     — TokenCounter factory (port/llm.TokenCounterFactory 実装)
//   - stream.go      — ストリーミング補完
//   - provider.go    — DI 用 Provider
//   - provider_registry.go — DB ベースの provider / model / role 管理
package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	anyllm "github.com/mozilla-ai/any-llm-go"
	llmerrors "github.com/mozilla-ai/any-llm-go/errors"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/mozilla-ai/any-llm-go/providers/gemini"
	"github.com/mozilla-ai/any-llm-go/providers/openai"
	"go.opentelemetry.io/otel/trace"

	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// Response / RawMessage は port/llm の正準定義への型エイリアス。
// 既存呼び出し側 (`llm.Response` / `llm.RawMessage`) を温存する。
type (
	Response   = portllm.Response
	RawMessage = portllm.RawMessage
)

// roleProvider はロールに割り当てられたプロバイダの状態。
type roleProvider struct {
	provider     providers.Provider
	providerName string
	model        string
	apiBase      string
	maxCtx       int
	capabilities []string
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
	roles map[string]roleProvider

	// Embedding は Embedder インターフェース経由のため据え置き。
	embeddingProv  providers.Provider
	embeddingModel string
	embeddingDims  int

	logger *slog.Logger
	tracer trace.Tracer
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
			"background":   mainRP,
		},
		embeddingModel: emb.Model,
		embeddingDims:  emb.Dims,
		logger:         logger,
	}

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

// SwapRoleSpec はロールのプロバイダを portllm.RoleSpec で切り替える。
func (c *Client) SwapRoleSpec(role string, spec portllm.RoleSpec) {
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

// --- shared helpers ---

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

	if cleaned == "" && reasoning != "" {
		cleaned = reasoning
		reasoning = ""
	}

	return reasoning, cleaned
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
