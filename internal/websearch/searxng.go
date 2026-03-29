package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
)

const maxFetchBytes = 512 * 1024 // 512KB raw HTML limit

// SearchResult represents a single search result from SearXNG.
type SearchResult struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Content   string `json:"content"`   // snippet
	Engine    string `json:"engine"`
	Thumbnail string `json:"thumbnail"` // thumbnail URL (optional)
	ImgSrc    string `json:"img_src"`   // full image URL (optional, images category)
}

// SearXNGClient queries a self-hosted SearXNG instance.
type SearXNGClient struct {
	baseURL string
	client  *http.Client
}

// NewSearXNG creates a SearXNG client for the given base URL.
func NewSearXNG(baseURL string) *SearXNGClient {
	return &SearXNGClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Search performs a search and returns up to limit results.
func (c *SearXNGClient) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("searxng: URLのパースに失敗: %w", err)
	}
	u.Path = "/search"
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("language", "ja")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("searxng: リクエストの作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: 取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng: ステータス %d", resp.StatusCode)
	}

	var body struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("searxng: デコードに失敗: %w", err)
	}

	if limit > 0 && len(body.Results) > limit {
		body.Results = body.Results[:limit]
	}
	return body.Results, nil
}

// FetchPage fetches a web page, extracts the main content using readability,
// and returns plain text (truncated to maxRunes).
// Times out after 8 seconds to avoid slow pages blocking the pipeline.
func (c *SearXNGClient) FetchPage(ctx context.Context, pageURL string, maxRunes int) (string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("fetch page: リクエストの作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; suzuha-bot/1.0)")
	req.Header.Set("Accept-Language", "ja,en;q=0.5")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch page: 取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch page: ステータス %d", resp.StatusCode)
	}

	parsedURL, _ := url.Parse(pageURL)
	article, err := readability.FromReader(io.LimitReader(resp.Body, maxFetchBytes), parsedURL)
	if err != nil {
		return "", fmt.Errorf("fetch page: 本文抽出に失敗: %w", err)
	}

	var buf strings.Builder
	if err := article.RenderText(&buf); err != nil {
		return "", fmt.Errorf("fetch page: テキスト変換に失敗: %w", err)
	}
	text := CollapseWhitespace(buf.String())

	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes])
	}
	return text, nil
}

// CollapseWhitespace reduces runs of 3+ newlines to 2.
func CollapseWhitespace(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// TruncateResults formats search results for display in prompts.
func TruncateResults(results []SearchResult, maxPerResult int) string {
	var s string
	for i, r := range results {
		snippet := r.Content
		if rs := []rune(snippet); len(rs) > maxPerResult {
			snippet = string(rs[:maxPerResult]) + "..."
		}
		s += strconv.Itoa(i+1) + ". " + r.Title + "\n   " + snippet + "\n"
	}
	return s
}
