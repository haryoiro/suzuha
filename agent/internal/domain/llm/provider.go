// Package llm は LLM プロバイダ管理のドメイン型を定義する。
// port/llm と admin API から共有されるため domain/ に配置する。
// RoleSpec (providers.Provider インスタンスを保持) は外部 SDK 依存のため
// 本 package には含めず、internal/llm 側に残す。
package llm

import "slices"

// ProviderEntry はプロバイダ接続情報。
type ProviderEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`              // "openai", "zhipu", "gemini", "qwen"
	APIKey  string `json:"api_key,omitempty"` // メモリ上は平文
	APIBase string `json:"api_base"`
	Source  string `json:"source"` // "seed" or "user"
}

// ModelInfo はモデルカタログのエントリ。
type ModelInfo struct {
	ProviderName string   `json:"provider_name"`
	ModelID      string   `json:"model_id"`
	Capabilities []string `json:"capabilities"` // ["text"], ["text","vision"]
	MaxContext   int      `json:"max_context"`
	Source       string   `json:"source"` // "static", "api", "user"
}

// HasCapability はモデルが指定 capability を持つか返す。
func (m *ModelInfo) HasCapability(cap string) bool {
	return slices.Contains(m.Capabilities, cap)
}

// RoleAssignment はロールへのプロバイダ/モデル割り当て。
type RoleAssignment struct {
	Role         string `json:"role"`
	ProviderName string `json:"provider_name"`
	ModelID      string `json:"model_id"`
}
