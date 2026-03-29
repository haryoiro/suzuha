package research

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/websearch"
)

// ExploreTool triggers a fast deep-research session.
// Parallel searches + parallel fetches + 2 LLM calls only.
type ExploreTool struct {
	searx        *websearch.SearXNGClient
	llm          *llm.Client
	systemPrompt string
	breadth      int
	maxSources   int
}

var _ tool.Tool = (*ExploreTool)(nil)

// NewExploreTool creates a fast explore tool.
func NewExploreTool(searxngURL string, llmClient *llm.Client, systemPrompt string, breadth, maxSources int) *ExploreTool {
	if breadth <= 0 {
		breadth = defaultBreadth
	}
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}
	return &ExploreTool{
		searx:        websearch.NewSearXNG(searxngURL),
		llm:          llmClient,
		systemPrompt: systemPrompt,
		breadth:      breadth,
		maxSources:   maxSources,
	}
}

func (t *ExploreTool) Name() string { return "research" }
func (t *ExploreTool) Description() string {
	return "トピックについて高速にリサーチする。複数の検索を並列で行い、多角的な情報を集めて統合する。何かについてしっかり調べたい時に使う。結果はメモリに保存されるが、みんなには共有されていないので共有したかったら知ったことを共有しよう。"
}

func (t *ExploreTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "リサーチしたいトピックやキーワード。省略するとランダムなWikipedia記事から始まる。"
			},
			"breadth": {
				"type": "integer",
				"description": "サブクエリの数（多いほど広く調べる）。デフォルト3。"
			},
			"max_sources": {
				"type": "integer",
				"description": "取得するページの最大数。デフォルト6。"
			}
		}
	}`)
}

type exploreInput struct {
	Query      string `json:"query"`
	Breadth    int    `json:"breadth"`
	MaxSources int    `json:"max_sources"`
}

func (t *ExploreTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in exploreInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}

	breadth := t.breadth
	if in.Breadth > 0 {
		breadth = in.Breadth
		if breadth > 10 {
			breadth = 10
		}
	}
	maxSources := t.maxSources
	if in.MaxSources > 0 {
		maxSources = in.MaxSources
		if maxSources > 20 {
			maxSources = 20
		}
	}

	summary, err := t.doResearch(ctx, in.Query, breadth, maxSources)
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("research: リサーチに失敗しました: %v", err)), nil
	}
	return tool.TextResult(summary), nil
}

func (t *ExploreTool) doResearch(ctx context.Context, query string, breadth, maxSources int) (string, error) {
	// If no query, start from random Wikipedia article.
	if query == "" {
		article, err := websearch.RandomArticle(ctx)
		if err != nil {
			return "", fmt.Errorf("research: Wikipediaランダム記事の取得に失敗: %w", err)
		}
		query = article.Title
	}

	// Step 1: LLM expands query into diverse sub-queries.
	queries, err := expandQuery(ctx, t.llm, t.systemPrompt, query, breadth)
	if err != nil {
		queries = []string{query}
	}

	// Step 2: Parallel search all sub-queries.
	results := searchAll(ctx, t.searx, queries, searchPerQuery)
	if len(results) == 0 {
		return "検索結果が見つからなかった", nil
	}

	// Step 3: Parallel fetch top pages.
	sources := fetchAll(ctx, t.searx, results, maxSources, pageMaxRunes)
	if len(sources) == 0 {
		return "ページの取得に失敗した", nil
	}

	// Build raw result for agent to process with conversation context.
	// No LLM synthesis here — suzuha handles interpretation.
	// Results stay in context until compaction, where the consolidator
	// extracts any important knowledge as long-term memories.
	return formatRawSources(query, queries, sources), nil
}
