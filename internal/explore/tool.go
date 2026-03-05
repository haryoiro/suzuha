package explore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
)

// ExploreTool allows the LLM to trigger a web exploration session.
type ExploreTool struct {
	searx        *SearXNGClient
	llm          *llm.Client
	mem          memory.Store
	systemPrompt string
	maxDepth     int
}

var _ tool.Tool = (*ExploreTool)(nil)

// NewExploreTool creates an explore tool.
func NewExploreTool(searxngURL string, llmClient *llm.Client, memStore memory.Store, systemPrompt string, maxDepth int) *ExploreTool {
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	return &ExploreTool{
		searx:        NewSearXNG(searxngURL),
		llm:          llmClient,
		mem:          memStore,
		systemPrompt: systemPrompt,
		maxDepth:     maxDepth,
	}
}

func (t *ExploreTool) Name() string { return "explore" }
func (t *ExploreTool) Description() string {
	return "ネットを散歩して情報を探索する。気になるトピックから出発して関連情報を芋づる式にたどる。結果はメモリに保存される。"
}

func (t *ExploreTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "探索の出発点となるキーワードやトピック。省略するとランダムなWikipedia記事から始まる。"
			}
		}
	}`)
}

type exploreToolInput struct {
	Query string `json:"query"`
}

func (t *ExploreTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in exploreToolInput
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}

	summary, err := t.doExploration(ctx, in.Query)
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("explore: 探索に失敗しました: %v", err)), nil
	}
	return tool.TextResult(summary), nil
}

func (t *ExploreTool) doExploration(ctx context.Context, startQuery string) (string, error) {
	var title, content string

	if startQuery != "" {
		results, err := t.searx.Search(ctx, startQuery, searchResultsMax)
		if err != nil || len(results) == 0 {
			article, wErr := RandomArticle(ctx)
			if wErr != nil {
				return "", fmt.Errorf("explore: 検索結果がなくWikipediaも失敗しました: %w", wErr)
			}
			title = article.Title
			content = article.Extract
		} else {
			title = results[0].Title
			content = results[0].Content
		}
	} else {
		article, err := RandomArticle(ctx)
		if err != nil {
			return "", fmt.Errorf("explore: Wikipediaランダム記事の取得に失敗: %w", err)
		}
		title = article.Title
		content = article.Extract
	}

	var path []hop
	var rememberedItems []string

	for depth := 0; depth < t.maxDepth; depth++ {
		eval, err := evaluate(ctx, t.llm, t.systemPrompt, title, content, path)
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
		if depth == t.maxDepth-1 {
			break
		}

		results, err := t.searx.Search(ctx, *eval.NextQuery, searchResultsMax)
		if err != nil || len(results) == 0 {
			break
		}

		picked, err := pickResult(ctx, t.llm, t.systemPrompt, *eval.NextQuery, results, path)
		if err != nil || picked == nil {
			break
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

	summary := buildSummary(path, rememberedItems)
	if t.mem != nil {
		mem := &memory.Memory{
			Type:    memory.MemoryTypeWorld,
			Content: summary,
			Metadata: map[string]any{
				"source": "explore_tool",
				"type":   "summary",
			},
		}
		_ = t.mem.Save(ctx, mem)
	}

	return summary, nil
}
