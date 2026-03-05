package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// SearchResult represents a single search result from SearXNG.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"` // snippet
	Engine  string `json:"engine"`
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

// FetchPage retrieves a web page and returns its text content (truncated).
func (c *SearXNGClient) FetchPage(ctx context.Context, pageURL string, maxRunes int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("fetch page: リクエストの作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch page: 取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch page: ステータス %d", resp.StatusCode)
	}

	// Read limited bytes to avoid huge pages.
	limited := make([]byte, maxRunes*4) // rough UTF-8 upper bound
	n, _ := resp.Body.Read(limited)
	text := string(limited[:n])

	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes])
	}
	return text, nil
}

// truncateResults formats search results for display in prompts.
func truncateResults(results []SearchResult, maxPerResult int) string {
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
