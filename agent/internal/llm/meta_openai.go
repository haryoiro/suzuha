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
	// API キー未設定時は /v1/models を叩かず静的カタログを返す。
	if apiKey == "" {
		return staticOpenAIModels(), nil
	}
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/models", nil)
	if err != nil {
		return staticOpenAIModels(), nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		// ネットワーク不達等では静的カタログにフォールバックする
		// (admin UI で Select の value が描画されない問題を避けるため)。
		return staticOpenAIModels(), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return staticOpenAIModels(), nil
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

// staticOpenAIModels は knownOpenAIModels を ModelInfo スライスに展開する。
// API 不達時のフォールバック用。
func staticOpenAIModels() []ModelInfo {
	out := make([]ModelInfo, 0, len(knownOpenAIModels))
	for id, info := range knownOpenAIModels {
		out = append(out, ModelInfo{
			ModelID:      id,
			Capabilities: info.Capabilities,
			MaxContext:   info.MaxContext,
			Source:       "static",
		})
	}
	return out
}
