package llm

import "context"

// ProviderMeta はプロバイダごとのモデルカタログ取得を抽象化する。
type ProviderMeta interface {
	// TypeName はこの meta が対応するプロバイダタイプ名を返す。
	TypeName() string
	// ListModels はプロバイダから利用可能なモデル一覧を取得する。
	ListModels(ctx context.Context, apiKey, apiBase string) ([]ModelInfo, error)
}

// providerMetaRegistry はプロバイダタイプごとの ProviderMeta を保持する。
var providerMetaRegistry = map[string]ProviderMeta{
	"openai": &openaiMeta{},
	"zhipu":  &zhipuMeta{},
	"gemini": &geminiMeta{},
	"qwen":   &openaiMeta{}, // OpenAI互換
}

// GetProviderMeta はプロバイダタイプに対応する ProviderMeta を返す。
func GetProviderMeta(providerType string) ProviderMeta {
	return providerMetaRegistry[providerType]
}
