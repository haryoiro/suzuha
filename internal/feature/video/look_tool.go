package video

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/tool"
)

// lookTool は動画の特定時点のフレームを VLM で描写するツール。
type lookTool struct {
	extractor transcript.FrameExtractor
	llmClient *llm.Client
	logger    *slog.Logger
}

// NewLookTool は video_look ツールを作成する。
func NewLookTool(extractor transcript.FrameExtractor, llmClient *llm.Client, logger *slog.Logger) tool.Tool {
	return &lookTool{extractor: extractor, llmClient: llmClient, logger: logger}
}

func (t *lookTool) Name() string    { return "video_look" }
func (t *lookTool) ReadOnly() bool { return true }

func (t *lookTool) Description() string {
	return "動画の特定時点のフレームを視覚的に確認する。video_watch の字幕で気になった箇所の映像を確認する際に使う。timestamp は \"1:23\" や秒数で指定。"
}

func (t *lookTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "動画の URL"
			},
			"timestamp": {
				"type": "string",
				"description": "見たい時点 (例: \"1:23\" or \"83\")"
			},
			"question": {
				"type": "string",
				"description": "何を見たいか (VLM へのプロンプト補足)"
			}
		},
		"required": ["url", "timestamp"]
	}`)
}

func (t *lookTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args struct {
		URL       string `json:"url"`
		Timestamp string `json:"timestamp"`
		Question  string `json:"question"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.ErrorResult("引数が不正です: " + err.Error()), nil
	}
	if args.URL == "" || args.Timestamp == "" {
		return tool.ErrorResult("url と timestamp は必須です"), nil
	}

	sec, err := parseTimestamp(args.Timestamp)
	if err != nil {
		return tool.ErrorResult("timestamp の形式が不正です: " + err.Error()), nil
	}

	t.logger.Info("video_look: フレーム取得開始", "url", args.URL, "timestamp", args.Timestamp, "sec", sec)

	// フレーム切り出し
	jpeg, err := t.extractor.ExtractFrame(ctx, args.URL, sec)
	if err != nil {
		t.logger.Warn("video_look: フレーム取得失敗", "url", args.URL, "error", err)
		return tool.ErrorResult(fmt.Sprintf("フレームの取得に失敗しました: %v", err)), nil
	}

	// VLM で描写
	dataURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)

	rc, inline := t.llmClient.WithCapability("conversation", "vision")
	if rc == nil {
		return tool.ErrorResult("ビジョンモデルが設定されていません"), nil
	}

	prompt := "この画像の内容を簡潔に描写してください。"
	if args.Question != "" {
		prompt = args.Question
	}

	if inline {
		// VLM 対応モデルなら画像を tool result に含める
		t.logger.Info("video_look: VLM inline で描写", "url", args.URL, "timestamp", args.Timestamp)
		return &tool.ToolResult{
			Content:   []tool.Content{{Text: fmt.Sprintf("[動画 %s のフレーム]\n%s", args.Timestamp, prompt)}},
			ImageURLs: []string{dataURI},
		}, nil
	}

	// 別モデルで描写
	description, err := t.llmClient.DescribeImage(ctx, dataURI, prompt)
	if err != nil {
		t.logger.Warn("video_look: VLM 描写失敗", "error", err)
		return tool.ErrorResult(fmt.Sprintf("画像の描写に失敗しました: %v", err)), nil
	}

	t.logger.Info("video_look: 描写完了", "url", args.URL, "timestamp", args.Timestamp, "desc_len", len(description))
	return tool.TextResult(fmt.Sprintf("[動画 %s のフレーム]\n%s", args.Timestamp, description)), nil
}

// parseTimestamp は "1:23" or "83" or "1:02:03" 形式をパースして秒数を返す。
func parseTimestamp(s string) (float64, error) {
	s = strings.TrimSpace(s)

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return strconv.ParseFloat(parts[0], 64)
	case 2:
		min, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		sec, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		return min*60 + sec, nil
	case 3:
		h, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		min, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		sec, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
		return h*3600 + min*60 + sec, nil
	default:
		return 0, fmt.Errorf("不正な形式: %q", s)
	}
}
