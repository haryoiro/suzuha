package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	domainllm "github.com/haryoiro/suzuha/internal/domain/llm"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// Meta は OpenAI 互換 API のモデル一覧取得。
// /v1/models は ID しか返さないため、静的マッピングで capabilities と max_context を補完する。
type Meta struct{}

// NewMeta は Meta のインスタンスを返す。
func NewMeta() *Meta { return &Meta{} }

var _ portllm.ProviderMeta = (*Meta)(nil)

// knownModels は OpenAI のよく使われるモデルの静的メタデータ。
var knownModels = map[string]domainllm.ModelInfo{
	"gpt-4.1":      {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4.1-mini": {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4.1-nano": {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4o":       {Capabilities: []string{"text", "vision"}, MaxContext: 128000},
	"gpt-4o-mini":  {Capabilities: []string{"text", "vision"}, MaxContext: 128000},
	"o3":           {Capabilities: []string{"text", "vision"}, MaxContext: 200000},
	"o3-mini":      {Capabilities: []string{"text"}, MaxContext: 200000},
	"o4-mini":      {Capabilities: []string{"text", "vision"}, MaxContext: 200000},
}

// ListModels は OpenAI 互換 API からモデル一覧を取得する。
// API 不達時は静的カタログにフォールバックする。
func (m *Meta) ListModels(ctx context.Context, apiKey, apiBase string) ([]domainllm.ModelInfo, error) {
	if apiKey == "" {
		return staticModels(), nil
	}
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/models", nil)
	if err != nil {
		return staticModels(), nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return staticModels(), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return staticModels(), nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai: レスポンスの解析に失敗: %w", err)
	}

	var models []domainllm.ModelInfo
	for _, d := range result.Data {
		info := domainllm.ModelInfo{
			ModelID: d.ID,
			Source:  "api",
		}
		if known, ok := knownModels[d.ID]; ok {
			info.Capabilities = known.Capabilities
			info.MaxContext = known.MaxContext
		} else {
			info.Capabilities = []string{"text"}
		}
		models = append(models, info)
	}
	return models, nil
}

func staticModels() []domainllm.ModelInfo {
	out := make([]domainllm.ModelInfo, 0, len(knownModels))
	for id, info := range knownModels {
		out = append(out, domainllm.ModelInfo{
			ModelID:      id,
			Capabilities: info.Capabilities,
			MaxContext:   info.MaxContext,
			Source:       "static",
		})
	}
	return out
}
