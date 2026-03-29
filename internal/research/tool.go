package research

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/websearch"
)

// ResearchTool triggers a fast web research session.
// Single search + parallel page fetches, no LLM overhead.
type ResearchTool struct {
	searx      *websearch.SearXNGClient
	maxSources int
}

var _ tool.Tool = (*ResearchTool)(nil)

// NewResearchTool creates a research tool.
func NewResearchTool(searxngURL string, maxSources int) *ResearchTool {
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}
	return &ResearchTool{
		searx:      websearch.NewSearXNG(searxngURL),
		maxSources: maxSources,
	}
}

func (t *ResearchTool) Name() string { return "research" }
func (t *ResearchTool) Description() string {
	return "トピックについて高速にリサーチする。検索して上位ページの内容を取得する。何かについてしっかり調べたい時に使う。結果はみんなには共有されていないので共有したかったら知ったことを共有しよう。"
}

func (t *ResearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "リサーチしたいトピックやキーワード。省略するとランダムなWikipedia記事から始まる。"
			}
		}
	}`)
}

type researchInput struct {
	Query string `json:"query"`
}

func (t *ResearchTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in researchInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}

	result, err := t.doResearch(ctx, in.Query)
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("research: リサーチに失敗しました: %v", err)), nil
	}
	return tool.TextResult(result), nil
}

func (t *ResearchTool) doResearch(ctx context.Context, query string) (string, error) {
	if query == "" {
		article, err := websearch.RandomArticle(ctx)
		if err != nil {
			return "", fmt.Errorf("research: Wikipediaランダム記事の取得に失敗: %w", err)
		}
		query = article.Title
	}

	// Single search, no LLM sub-query expansion.
	results, err := t.searx.Search(ctx, query, searchPerQuery)
	if err != nil || len(results) == 0 {
		return "検索結果が見つからなかった", nil
	}

	// Parallel fetch top pages.
	sources := fetchAll(ctx, t.searx, results, t.maxSources, pageMaxRunes)
	if len(sources) == 0 {
		return "ページの取得に失敗した", nil
	}

	return formatSources(query, sources), nil
}
