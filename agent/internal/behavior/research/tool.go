package research

import (
	"context"
	"encoding/json"

	"github.com/haryoiro/suzuha/internal/tool"
)

// ResearchTool triggers a fast web research session.
// Search → fetch top pages in parallel. No LLM overhead.
type ResearchTool struct {
	searx      *SearXNGClient
	maxSources int
}

var _ tool.Tool = (*ResearchTool)(nil)

// NewResearchTool creates a research tool.
func NewResearchTool(searxngURL string, maxSources int) *ResearchTool {
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}
	return &ResearchTool{
		searx:      NewSearXNG(searxngURL),
		maxSources: maxSources,
	}
}

func (t *ResearchTool) Name() string   { return "research" }
func (t *ResearchTool) ReadOnly() bool { return true }
func (t *ResearchTool) Description() string {
	return "トピックについて高速にリサーチする。検索して上位ページの内容を取得する。何かについてしっかり調べたい時に使う。結果はみんなには共有されていないので共有したかったら知ったことを共有しよう。"
}

func (t *ResearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "リサーチしたいトピックやキーワード"
			}
		},
		"required": ["query"]
	}`)
}

type researchInput struct {
	Query string `json:"query"`
}

func (t *ResearchTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in researchInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return tool.ErrorResult("無効な入力: " + err.Error()), nil
		}
	}
	if in.Query == "" {
		return tool.ErrorResult("query は必須です"), nil
	}

	// Search → fetch top pages.
	results, err := t.searx.Search(ctx, in.Query, searchResults)
	if err != nil || len(results) == 0 {
		return tool.ErrorResult("検索結果が見つからなかった"), nil
	}

	sources := fetchAll(ctx, t.searx, results, t.maxSources, pageMaxRunes)
	if len(sources) == 0 {
		return tool.ErrorResult("ページの取得に失敗した"), nil
	}

	return tool.TextResult(formatSources(in.Query, sources)), nil
}
