package research

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/websearch"
)

const (
	defaultMaxSources = 5    // pages to fetch in parallel
	pageMaxRunes      = 1200 // per-page content limit
	searchResults     = 20   // search results to retrieve (filtered before fetch)
)

// source holds fetched page content.
type source struct {
	Title   string
	URL     string
	Content string
}

// skipExtensions are file extensions that should not be fetched.
var skipExtensions = []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".tar", ".gz"}

// filterHTMLResults removes non-HTML URLs (PDF, docs, etc.) from search results.
func filterHTMLResults(results []websearch.SearchResult) []websearch.SearchResult {
	var out []websearch.SearchResult
	for _, r := range results {
		lower := strings.ToLower(r.URL)
		skip := false
		for _, ext := range skipExtensions {
			if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, r)
		}
	}
	return out
}

// fetchAll fetches multiple pages in parallel using readability.
func fetchAll(
	ctx context.Context,
	searx *websearch.SearXNGClient,
	results []websearch.SearchResult,
	maxSources int,
	maxRunes int,
) []source {
	results = filterHTMLResults(results)
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
