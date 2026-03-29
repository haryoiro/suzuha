package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
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
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *Fetch) Name() string { return "fetch" }
func (f *Fetch) Description() string {
	return "URLの内容を取得してテキストで返す。Webページは本文が自動で抽出される。"
}

func (f *Fetch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "The URL to fetch."}
		},
		"required": ["url"]
	}`)
}

type fetchInput struct {
	URL string `json:"url"`
}

func (f *Fetch) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in fetchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return tool.ErrorResult("不正なリクエスト: " + err.Error()), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; suzuha-bot/1.0)")
	req.Header.Set("Accept-Language", "ja,en;q=0.5")

	resp, err := f.client.Do(req)
	if err != nil {
		return tool.ErrorResult("フェッチ失敗: " + err.Error()), nil
	}
	defer resp.Body.Close()

	// Use readability to extract main content.
	parsedURL, _ := url.Parse(in.URL)
	article, err := readability.FromReader(io.LimitReader(resp.Body, maxBodyBytes), parsedURL)
	if err != nil {
		return tool.ErrorResult("本文抽出に失敗: " + err.Error()), nil
	}

	var buf strings.Builder
	if err := article.RenderText(&buf); err != nil {
		return tool.ErrorResult("テキスト変換に失敗: " + err.Error()), nil
	}
	text := strings.TrimSpace(buf.String())

	// Truncate to keep LLM context manageable.
	runes := []rune(text)
	if len(runes) > maxOutputRunes {
		text = string(runes[:maxOutputRunes]) + "\n\n...(省略)"
	}

	return tool.TextResult(text), nil
}

var _ tool.Tool = (*Fetch)(nil)
