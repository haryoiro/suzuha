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

const jinaReaderPrefix = "https://r.jina.ai/"

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
	return "URLの内容を取得してMarkdownで返す。Webページは自動で整形される。"
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
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	if in.Method == "" {
		in.Method = "GET"
	}

	// Use r.jina.ai reader for GET requests to get clean Markdown.
	fetchURL := in.URL
	if in.Method == "GET" {
		fetchURL = jinaReaderPrefix + in.URL
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, fetchURL, nil)
	if err != nil {
		return tool.ErrorResult("不正なリクエスト: " + err.Error()), nil
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0 (https://github.com/haryoiro/suzuha)")

	resp, err := f.client.Do(req)
	if err != nil {
		return tool.ErrorResult("フェッチ失敗: " + err.Error()), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return tool.ErrorResult("レスポンス読み取り失敗: " + err.Error()), nil
	}

	text := string(body)

	// Truncate to keep LLM context manageable.
	runes := []rune(text)
	if len(runes) > maxOutputRunes {
		text = string(runes[:maxOutputRunes]) + "\n\n...(省略)"
	}

	return tool.TextResult(fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, text)), nil
}

var _ tool.Tool = (*Fetch)(nil)
