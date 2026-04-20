package zhipu

import (
	"context"

	domainllm "github.com/haryoiro/suzuha/internal/domain/llm"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// Meta は ZhiPu (GLM) のモデルカタログ。
// API がないため静的リストを返す。
type Meta struct{}

// NewMeta は Meta のインスタンスを返す。
func NewMeta() *Meta { return &Meta{} }

var _ portllm.ProviderMeta = (*Meta)(nil)

var knownModels = []domainllm.ModelInfo{
	{ModelID: "glm-4.7", Capabilities: []string{"text"}, MaxContext: 200000},
	{ModelID: "glm-5.1", Capabilities: []string{"text"}, MaxContext: 200000},
	{ModelID: "glm-4-flash", Capabilities: []string{"text"}, MaxContext: 128000},
	{ModelID: "glm-4v-plus", Capabilities: []string{"text", "vision"}, MaxContext: 16000},
	{ModelID: "glm-4-flashx", Capabilities: []string{"text"}, MaxContext: 128000},
}

// ListModels は静的なモデル一覧を返す。
func (m *Meta) ListModels(_ context.Context, _, _ string) ([]domainllm.ModelInfo, error) {
	out := make([]domainllm.ModelInfo, len(knownModels))
	for i, model := range knownModels {
		out[i] = model
		out[i].Source = "static"
	}
	return out, nil
}
