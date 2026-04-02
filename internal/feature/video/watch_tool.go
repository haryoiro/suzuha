package video

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/internal/tool"
)

const maxTranscriptLen = 8000 // LLM コンテキストに収まるように字幕を truncate

// watchTool は動画の字幕を取得して内容を理解するツール。
type watchTool struct {
	fetcher transcript.Fetcher
	logger  *slog.Logger
}

// NewWatchTool は video_watch ツールを作成する。
func NewWatchTool(fetcher transcript.Fetcher, logger *slog.Logger) tool.Tool {
	return &watchTool{fetcher: fetcher, logger: logger}
}

func (t *watchTool) Name() string    { return "video_watch" }
func (t *watchTool) ReadOnly() bool { return true }

func (t *watchTool) Description() string {
	return "動画の字幕を取得して内容を理解する。YouTube, ニコニコ動画, Twitch 等の動画 URL に対応。会話に動画 URL が出てきて内容を知りたいときに使う。"
}

func (t *watchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "動画の URL"
			},
			"lang": {
				"type": "string",
				"description": "字幕言語 (デフォルト: ja)"
			}
		},
		"required": ["url"]
	}`)
}

func (t *watchTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args struct {
		URL  string `json:"url"`
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.ErrorResult("引数が不正です: " + err.Error()), nil
	}

	if args.URL == "" {
		return tool.ErrorResult("url は必須です"), nil
	}

	langs := []string{"ja", "en"}
	if args.Lang != "" {
		langs = []string{args.Lang, "ja", "en"}
	}

	t.logger.Info("video_watch: 字幕取得開始", "url", args.URL, "langs", langs)

	info, lines, err := t.fetcher.Fetch(ctx, args.URL, langs)
	if err != nil {
		t.logger.Warn("video_watch: 字幕取得に失敗", "url", args.URL, "error", err)
		return tool.ErrorResult(fmt.Sprintf("字幕の取得に失敗しました: %v", err)), nil
	}

	if len(lines) == 0 {
		return tool.ErrorResult("この動画には字幕がありません"), nil
	}

	text := transcript.FormatTranscript(info, lines, maxTranscriptLen)
	t.logger.Info("video_watch: 字幕取得完了", "url", args.URL, "title", info.Title, "lines", len(lines))

	return tool.TextResult(text), nil
}
