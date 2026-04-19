package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// geminiMeta は Google Gemini API からモデルカタログを動的取得する。
// Gemini は唯一 capabilities と context window を API から返すプロバイダ。
type geminiMeta struct{}

func (m *geminiMeta) ListModels(ctx context.Context, apiKey, _ string) ([]ModelInfo, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini: API キーが必要です")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: モデル一覧の取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: モデル一覧 API がステータス %d を返しました", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name                     string   `json:"name"`
			DisplayName              string   `json:"displayName"`
			InputTokenLimit          int      `json:"inputTokenLimit"`
			OutputTokenLimit         int      `json:"outputTokenLimit"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("gemini: レスポンスの解析に失敗: %w", err)
	}

	var models []ModelInfo
	for _, gm := range result.Models {
		// "models/gemini-2.0-flash" → "gemini-2.0-flash"
		modelID := strings.TrimPrefix(gm.Name, "models/")

		caps := []string{"text"}
		// Gemini の vision 対応はモデル名から推定 (API に明示フィールドがない)
		if strings.Contains(modelID, "vision") || strings.Contains(gm.DisplayName, "Vision") {
			caps = append(caps, "vision")
		}
		// generateContent をサポートするモデルのみ
		hasGenerate := false
		for _, method := range gm.SupportedGenerationMethods {
			if method == "generateContent" {
				hasGenerate = true
				break
			}
		}
		if !hasGenerate {
			continue
		}

		models = append(models, ModelInfo{
			ModelID:      modelID,
			Capabilities: caps,
			MaxContext:    gm.InputTokenLimit,
			Source:        "api",
		})
	}
	return models, nil
}
