package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/websearch"
	"github.com/mozilla-ai/any-llm-go/providers"
)

const (
	defaultBreadth    = 3    // sub-queries to generate
	defaultMaxSources = 6    // pages to fetch in parallel
	pageMaxRunes      = 1200 // per-page content limit
	searchPerQuery    = 5    // results per sub-query
)

// source holds fetched page content.
type source struct {
	Title   string
	URL     string
	Content string
}

// expandQuery asks the LLM to generate diverse sub-queries for deep research.
// This is the only LLM call in the research pipeline.
func expandQuery(
	ctx context.Context,
	llmClient *llm.Client,
	systemPrompt string,
	query string,
	breadth int,
) ([]string, error) {
	prompt := fmt.Sprintf(
		`「%s」についてリサーチしたい。多角的に調べるための検索クエリを%d個考えて。
異なる視点・切り口のクエリにすること。

JSON配列だけ出力して:
["クエリ1", "クエリ2", ...]`, query, breadth)

	messages := []providers.Message{
		{Role: "user", Content: prompt},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := llmClient.CompleteRawDefault(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("expand query: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	text = stripCodeFence(text)

	var queries []string
	if err := json.Unmarshal([]byte(text), &queries); err != nil {
		return []string{query}, nil
	}
	if len(queries) == 0 {
		return []string{query}, nil
	}
	return queries, nil
}

// searchAll runs multiple SearXNG searches in parallel, deduplicates by URL.
func searchAll(
	ctx context.Context,
	searx *websearch.SearXNGClient,
	queries []string,
	perQuery int,
) []websearch.SearchResult {
	type result struct {
		results []websearch.SearchResult
	}

	ch := make(chan result, len(queries))
	for _, q := range queries {
		go func(query string) {
			results, err := searx.Search(ctx, query, perQuery)
			if err != nil {
				ch <- result{}
				return
			}
			ch <- result{results: results}
		}(q)
	}

	seen := make(map[string]bool)
	var all []websearch.SearchResult
	for range queries {
		r := <-ch
		for _, sr := range r.results {
			if !seen[sr.URL] {
				seen[sr.URL] = true
				all = append(all, sr)
			}
		}
	}
	return all
}

// fetchAll fetches multiple pages in parallel via Jina reader.
func fetchAll(
	ctx context.Context,
	searx *websearch.SearXNGClient,
	results []websearch.SearchResult,
	maxSources int,
	maxRunes int,
) []source {
	if len(results) > maxSources {
		results = results[:maxSources]
	}

	sources := make([]source, len(results))
	var wg sync.WaitGroup
	for i, r := range results {
		wg.Add(1)
		go func(idx int, sr websearch.SearchResult) {
			defer wg.Done()
			content, err := searx.FetchPage(ctx, sr.URL, maxRunes)
			if err != nil || content == "" {
				content = sr.Content // fallback to snippet
			}
			sources[idx] = source{
				Title:   sr.Title,
				URL:     sr.URL,
				Content: content,
			}
		}(i, r)
	}
	wg.Wait()

	var valid []source
	for _, s := range sources {
		if s.Content != "" {
			valid = append(valid, s)
		}
	}
	return valid
}

// formatRawSources formats collected sources as raw tool output.
// No LLM synthesis — the agent processes this with full conversation context.
func formatRawSources(query string, subQueries []string, sources []source) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "「%s」について%d件のソースを収集した。\n", query, len(sources))
	fmt.Fprintf(&sb, "検索クエリ: %s\n\n", strings.Join(subQueries, " / "))

	for i, s := range sources {
		fmt.Fprintf(&sb, "--- [%d] %s ---\n", i+1, s.Title)
		fmt.Fprintf(&sb, "URL: %s\n", s.URL)
		sb.WriteString(truncateRunes(s.Content, pageMaxRunes))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
