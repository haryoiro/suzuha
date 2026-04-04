package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// openaiMeta は OpenAI 互換 API のモデル一覧取得。
// /v1/models は ID しか返さないため、静的マッピングで capabilities と max_context を補完する。
type openaiMeta struct{}

func (m *openaiMeta) TypeName() string { return "openai" }

// knownOpenAIModels は OpenAI のよく使われるモデルの静的メタデータ。
var knownOpenAIModels = map[string]ModelInfo{
	"gpt-4.1":            {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4.1-mini":       {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4.1-nano":       {Capabilities: []string{"text", "vision"}, MaxContext: 1047576},
	"gpt-4o":             {Capabilities: []string{"text", "vision"}, MaxContext: 128000},
	"gpt-4o-mini":        {Capabilities: []string{"text", "vision"}, MaxContext: 128000},
	"o3":                 {Capabilities: []string{"text", "vision"}, MaxContext: 200000},
	"o3-mini":            {Capabilities: []string{"text"}, MaxContext: 200000},
	"o4-mini":            {Capabilities: []string{"text", "vision"}, MaxContext: 200000},
}

func (m *openaiMeta) ListModels(ctx context.Context, apiKey, apiBase string) ([]ModelInfo, error) {
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: モデル一覧の取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: モデル一覧 API がステータス %d を返しました", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai: レスポンスの解析に失敗: %w", err)
	}

	var models []ModelInfo
	for _, d := range result.Data {
		info := ModelInfo{
			ModelID: d.ID,
			Source:  "api",
		}
		if known, ok := knownOpenAIModels[d.ID]; ok {
			info.Capabilities = known.Capabilities
			info.MaxContext = known.MaxContext
		} else {
			info.Capabilities = []string{"text"}
		}
		models = append(models, info)
	}
	return models, nil
}
