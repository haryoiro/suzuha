package wander

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/websearch"
)

// WanderTool allows the LLM to trigger a casual web wandering session.
type WanderTool struct {
	searx        *websearch.SearXNGClient
	llm          *llm.Client
	mem          memory.Store
	systemPrompt string
	maxDepth     int
}

var _ tool.Tool = (*WanderTool)(nil)

// NewWanderTool creates a wander tool.
func NewWanderTool(searxngURL string, llmClient *llm.Client, memStore memory.Store, systemPrompt string, maxDepth int) *WanderTool {
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	return &WanderTool{
		searx:        websearch.NewSearXNG(searxngURL),
		llm:          llmClient,
		mem:          memStore,
		systemPrompt: systemPrompt,
		maxDepth:     maxDepth,
	}
}

func (t *WanderTool) Name() string { return "wander" }
func (t *WanderTool) Description() string {
	return "ネットを散歩して情報を探索する。気になるトピックから出発して関連情報を芋づる式にたどる。ゆっくり深く探索したい時に使う。結果はメモリに保存されるが、みんなには共有されていないので共有したかったら知ったことを共有しよう。"
}

func (t *WanderTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "探索の出発点となるキーワードやトピック。省略するとランダムなWikipedia記事から始まる。"
			},
			"max_depth": {
				"type": "integer",
				"description": "最大ホップ数。深く掘りたいなら大きく、軽く見るだけなら小さく。省略時はデフォルト値。"
			}
		}
	}`)
}

type wanderToolInput struct {
	Query    string `json:"query"`
	MaxDepth int    `json:"max_depth"`
}

func (t *WanderTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in wanderToolInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}

	maxDepth := t.maxDepth
	if in.MaxDepth > 0 {
		maxDepth = in.MaxDepth
		if maxDepth > 20 {
			maxDepth = 20
		}
	}

	summary, err := t.doWander(ctx, in.Query, maxDepth)
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("wander: 散歩に失敗しました: %v", err)), nil
	}
	return tool.TextResult(summary), nil
}

func (t *WanderTool) doWander(ctx context.Context, startQuery string, maxDepth int) (string, error) {
	var title, content string

	if startQuery != "" {
		results, err := t.searx.Search(ctx, startQuery, searchResultsMax)
		if err != nil || len(results) == 0 {
			article, wErr := websearch.RandomArticle(ctx)
			if wErr != nil {
				return "", fmt.Errorf("wander: 検索結果がなくWikipediaも失敗しました: %w", wErr)
			}
			title = article.Title
			content = article.Extract
		} else {
			title = results[0].Title
			content = results[0].Content
		}
	} else {
		article, err := websearch.RandomArticle(ctx)
		if err != nil {
			return "", fmt.Errorf("wander: Wikipediaランダム記事の取得に失敗: %w", err)
		}
		title = article.Title
		content = article.Extract
	}

	var path []hop
	var rememberedItems []string

	for depth := 0; depth < maxDepth; depth++ {
		// Pre-search so LLM can evaluate + pick in one call.
		var searchResults []websearch.SearchResult
		if depth < maxDepth-1 {
			searchResults, _ = t.searx.Search(ctx, title, searchResultsMax)
		}

		eval, err := evaluateAndPick(ctx, t.llm, t.systemPrompt, title, content, path, searchResults)
		if err != nil {
			break
		}

		path = append(path, hop{Title: title, Impression: eval.Impression})
		if eval.Remember {
			rememberedItems = append(rememberedItems, fmt.Sprintf("%s — %s", title, eval.Impression))
		}

		if eval.NextQuery == nil || *eval.NextQuery == "" {
			break
		}
		if depth == maxDepth-1 {
			break
		}

		// Use LLM's pick if valid, otherwise search with next_query.
		var picked *websearch.SearchResult
		if eval.Pick > 0 && eval.Pick <= len(searchResults) {
			picked = &searchResults[eval.Pick-1]
		} else {
			results, sErr := t.searx.Search(ctx, *eval.NextQuery, searchResultsMax)
			if sErr != nil || len(results) == 0 {
				break
			}
			picked = &results[0]
		}

		pageContent, err := t.searx.FetchPage(ctx, picked.URL, contentMaxRunes)
		if err != nil {
			pageContent = picked.Content
		}
		title = picked.Title
		content = pageContent
	}

	if len(path) == 0 {
		return "何も見つからなかった", nil
	}

	summary, err := reflectOnExploration(ctx, t.llm, t.systemPrompt, path, rememberedItems)
	if err != nil || summary == "" {
		summary = buildSummary(path, rememberedItems)
	}
	if t.mem != nil {
		mem := &memory.Memory{
			Type:    memory.MemoryTypeWorld,
			Content: summary,
			Metadata: map[string]any{
				"source": "wander_tool",
				"type":   "reflection",
			},
		}
		_ = t.mem.Save(ctx, mem)
	}

	return summary, nil
}
