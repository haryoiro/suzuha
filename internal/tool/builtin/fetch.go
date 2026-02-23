package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// Fetch is a built-in tool that fetches a URL and returns the body.
type Fetch struct {
	client *http.Client
}

// NewFetch creates a Fetch tool with a default timeout.
func NewFetch() *Fetch {
	return &Fetch{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *Fetch) Name() string        { return "fetch" }
func (f *Fetch) Description() string { return "Fetch the contents of a URL and return the body text." }

func (f *Fetch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "The URL to fetch."},
			"method": {"type": "string", "enum": ["GET", "POST"], "default": "GET"}
		},
		"required": ["url"]
	}`)
}

type fetchInput struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

func (f *Fetch) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in fetchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}

	if in.Method == "" {
		in.Method = "GET"
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, nil)
	if err != nil {
		return tool.ErrorResult("bad request: " + err.Error()), nil
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return tool.ErrorResult("fetch failed: " + err.Error()), nil
	}
	defer resp.Body.Close()

	// Limit read to 1MB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tool.ErrorResult("read body: " + err.Error()), nil
	}

	return tool.TextResult(fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body))), nil
}

var _ tool.Tool = (*Fetch)(nil)
