package research

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/websearch"
)

// ResearchTool triggers a web research session.
// Search → LLM picks relevant results → parallel fetch picked pages.
type ResearchTool struct {
	searx      *websearch.SearXNGClient
	llm        *llm.Client
	maxSources int
}

var _ tool.Tool = (*ResearchTool)(nil)

// NewResearchTool creates a research tool.
func NewResearchTool(searxngURL string, llmClient *llm.Client, maxSources int) *ResearchTool {
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}
	return &ResearchTool{
		searx:      websearch.NewSearXNG(searxngURL),
		llm:        llmClient,
		maxSources: maxSources,
	}
}

func (t *ResearchTool) Name() string { return "research" }
func (t *ResearchTool) Description() string {
	return "トピックについてリサーチする。検索結果から関連性の高いページを選んで内容を取得する。何かについてしっかり調べたい時に使う。結果はみんなには共有されていないので共有したかったら知ったことを共有しよう。"
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

	// Step 1: Search.
	results, err := t.searx.Search(ctx, query, searchResults)
	if err != nil || len(results) == 0 {
		return "検索結果が見つからなかった", nil
	}

	// Step 2: LLM picks the most relevant results.
	picked := filterResults(results, pickResults(ctx, t.llm, query, results, t.maxSources))
	if len(picked) == 0 {
		return "関連する検索結果がなかった", nil
	}

	// Step 3: Parallel fetch picked pages.
	sources := fetchAll(ctx, t.searx, picked, pageMaxRunes)
	if len(sources) == 0 {
		return "ページの取得に失敗した", nil
	}

	return formatSources(query, sources), nil
}
