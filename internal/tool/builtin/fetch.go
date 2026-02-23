package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

const maxBodyBytes = 512 << 10 // 512KB raw read limit
const maxOutputRunes = 4000   // truncate final output for LLM context

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
func (f *Fetch) Description() string {
	return "Fetch a URL and return its content as Markdown. HTML pages are automatically cleaned (scripts, styles, navigation removed)."
}

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return tool.ErrorResult("read body: " + err.Error()), nil
	}

	ct := resp.Header.Get("Content-Type")
	text := string(body)

	// Convert HTML to Markdown.
	if strings.Contains(ct, "text/html") || strings.HasPrefix(text, "<!") || strings.HasPrefix(text, "<html") {
		text = htmlToMarkdown(text)
	}

	// Truncate to keep LLM context manageable.
	runes := []rune(text)
	if len(runes) > maxOutputRunes {
		text = string(runes[:maxOutputRunes]) + "\n\n...(truncated)"
	}

	return tool.TextResult(fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, text)), nil
}

var _ tool.Tool = (*Fetch)(nil)
