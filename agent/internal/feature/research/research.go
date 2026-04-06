package research

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/external/search"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
)

const (
	defaultMaxSources = 5    // pages to fetch in parallel
	pageMaxRunes      = 1200 // per-page content limit
	searchResults     = 20   // search results to retrieve (filtered before fetch)
)

// source holds fetched page content.
type source struct {
	Title     string
	URL       string
	Content   string
	TotalLen  int           // total runes before truncation
	FetchTime time.Duration // how long the fetch took
}

// skipExtensions are file extensions that should not be fetched.
var skipExtensions = []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".tar", ".gz"}

// filterHTMLResults removes non-HTML URLs (PDF, docs, etc.) from search results.
func filterHTMLResults(results []search.SearchResult) []search.SearchResult {
	var out []search.SearchResult
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
	searx *search.SearXNGClient,
	results []search.SearchResult,
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
		go func(idx int, sr search.SearchResult) {
			defer wg.Done()
			start := time.Now()
			content, err := searx.FetchPage(ctx, sr.URL, maxRunes)
			elapsed := time.Since(start)
			if err != nil || content == "" {
				content = sr.Content // fallback to snippet
			}
			totalLen := len([]rune(content))
			sources[idx] = source{
				Title:     sr.Title,
				URL:       sr.URL,
				Content:   content,
				TotalLen:  totalLen,
				FetchTime: elapsed,
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
		fmt.Fprintf(&sb, "URL: %s | fetch: %dms", s.URL, s.FetchTime.Milliseconds())
		truncated := len([]rune(s.Content)) > pageMaxRunes
		if truncated {
			remaining := s.TotalLen - pageMaxRunes
			fmt.Fprintf(&sb, " | 残り約%d文字省略", remaining)
		}
		sb.WriteString("\n")
		sb.WriteString(textutil.TruncateRunes(s.Content, pageMaxRunes))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

