package llm

import "github.com/mozilla-ai/any-llm-go/providers"

// RoleSpec は Client.SwapRoleSpec / Agent.OnRoleSpecChanged が共有する
// 解決済みロール仕様。providers.Provider インスタンスを直接保持するため
// capability/llm 固有の型に見えるが、runtime/agent や api 層からも参照されるので
// port 層に置く。
type RoleSpec struct {
	ProviderInst providers.Provider
	ProviderName string
	ProviderType string // "openai", "zhipu", "gemini", "qwen"
	ModelID      string
	APIBase      string
	MaxContext   int
	Capabilities []string
}
