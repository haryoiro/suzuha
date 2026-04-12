package llm

import "context"

// zhipuMeta は ZhiPu (GLM) のモデルカタログ。
// API がないため静的リストを返す。
type zhipuMeta struct{}

func (m *zhipuMeta) TypeName() string { return "zhipu" }

var knownZhipuModels = []ModelInfo{
	{ModelID: "glm-4.7", Capabilities: []string{"text"}, MaxContext: 200000},
	{ModelID: "glm-5.1", Capabilities: []string{"text"}, MaxContext: 200000},
	{ModelID: "glm-4-flash", Capabilities: []string{"text"}, MaxContext: 128000},
	{ModelID: "glm-4v-plus", Capabilities: []string{"text", "vision"}, MaxContext: 16000},
	{ModelID: "glm-4-flashx", Capabilities: []string{"text"}, MaxContext: 128000},
}

func (m *zhipuMeta) ListModels(_ context.Context, _, _ string) ([]ModelInfo, error) {
	out := make([]ModelInfo, len(knownZhipuModels))
	for i, model := range knownZhipuModels {
		out[i] = model
		out[i].Source = "static"
	}
	return out, nil
}
