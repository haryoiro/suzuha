package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// WebSearch is a built-in tool for web search.
// Uses a configurable search API endpoint (e.g. SearXNG, Brave Search API).
type WebSearch struct {
	apiURL string
	client *http.Client
}

// NewWebSearch creates a WebSearch tool.
// apiURL is the search API endpoint (e.g. "https://searxng.example.com/search").
func NewWebSearch(apiURL string) *WebSearch {
	return &WebSearch{
		apiURL: apiURL,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (w *WebSearch) Name() string        { return "web_search" }
func (w *WebSearch) Description() string { return "Search the web and return results." }

func (w *WebSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "The search query."},
			"max_results": {"type": "integer", "default": 5, "description": "Max results to return."}
		},
		"required": ["query"]
	}`)
}

type webSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func (w *WebSearch) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in webSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.MaxResults <= 0 {
		in.MaxResults = 5
	}

	u, err := url.Parse(w.apiURL)
	if err != nil {
		return tool.ErrorResult("bad api url: " + err.Error()), nil
	}
	q := u.Query()
	q.Set("q", in.Query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return tool.ErrorResult("request build: " + err.Error()), nil
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return tool.ErrorResult("search failed: " + err.Error()), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return tool.ErrorResult("read body: " + err.Error()), nil
	}

	// Parse SearXNG-style JSON response.
	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		// Return raw body if parsing fails.
		return tool.TextResult(fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body))), nil
	}

	var text string
	for i, r := range result.Results {
		if i >= in.MaxResults {
			break
		}
		text += fmt.Sprintf("[%d] %s\n    %s\n    %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	if text == "" {
		text = "No results found."
	}

	return tool.TextResult(text), nil
}

var _ tool.Tool = (*WebSearch)(nil)
