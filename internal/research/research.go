package research

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/websearch"
	"github.com/mozilla-ai/any-llm-go/providers"
)

const (
	defaultMaxSources = 4    // pages to fetch after LLM pick
	pageMaxRunes      = 1200 // per-page content limit
	searchResults     = 10   // search results to retrieve for LLM to pick from
	snippetMaxRunes   = 120  // snippet length in pick prompt
)

// source holds fetched page content.
type source struct {
	Title   string
	URL     string
	Content string
}

// pickResults asks the LLM to select the most relevant search results for the query.
// Returns indices (0-based) of picked results.
func pickResults(
	ctx context.Context,
	llmClient *llm.Client,
	query string,
	results []websearch.SearchResult,
	maxPick int,
) []int {
	var sb strings.Builder
	fmt.Fprintf(&sb, "「%s」で検索した結果:\n\n", query)
	for i, r := range results {
		snippet := truncateRunes(r.Content, snippetMaxRunes)
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, r.Title, snippet)
	}
	fmt.Fprintf(&sb, "\nこの中から「%s」に最も関連するものを最大%d件選んで。\n", query, maxPick)
	sb.WriteString("番号をカンマ区切りで出力して（例: 1,3,5）。どれも関連なければ「なし」と答えて。\n")

	resp, err := llmClient.CompleteRawDefault(ctx, []providers.Message{
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		// Fallback: return first maxPick indices.
		return fallbackIndices(len(results), maxPick)
	}

	indices := parsePickResponse(resp.Text, len(results))
	if len(indices) == 0 {
		return fallbackIndices(len(results), maxPick)
	}
	if len(indices) > maxPick {
		indices = indices[:maxPick]
	}
	return indices
}

// parsePickResponse extracts 1-based indices from LLM response, returns 0-based.
func parsePickResponse(text string, maxIdx int) []int {
	var indices []int
	seen := make(map[int]bool)
	num := 0
	inNum := false
	for _, r := range text {
		if r >= '0' && r <= '9' {
			num = num*10 + int(r-'0')
			inNum = true
		} else {
			if inNum && num >= 1 && num <= maxIdx && !seen[num-1] {
				seen[num-1] = true
				indices = append(indices, num-1)
			}
			num = 0
			inNum = false
		}
	}
	// Handle trailing number.
	if inNum && num >= 1 && num <= maxIdx && !seen[num-1] {
		indices = append(indices, num-1)
	}
	return indices
}

// fallbackIndices returns [0, 1, ..., min(n, max)-1].
func fallbackIndices(n, max int) []int {
	if n > max {
		n = max
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// filterResults selects results at the given indices.
func filterResults(results []websearch.SearchResult, indices []int) []websearch.SearchResult {
	out := make([]websearch.SearchResult, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < len(results) {
			out = append(out, results[idx])
		}
	}
	return out
}

// fetchAll fetches multiple pages in parallel via Jina reader.
func fetchAll(
	ctx context.Context,
	searx *websearch.SearXNGClient,
	results []websearch.SearchResult,
	maxRunes int,
) []source {
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

// formatSources formats collected sources as tool output.
func formatSources(query string, sources []source) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "「%s」について%d件のソースを収集した。\n\n", query, len(sources))

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

