package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/port/tool"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// skipResponseTool は LLM が応答をスキップしたいことを示す仮想ツール。
// グローバルな Registry には登録せず、directive に応じて
// completeWithTools の呼び出し時にのみ注入される。
type skipResponseTool struct{}

var _ tool.Tool = skipResponseTool{}

func (skipResponseTool) Name() string   { return "skip_response" }
func (skipResponseTool) ReadOnly() bool { return true }
func (skipResponseTool) Description() string {
	return "この会話に返答しないときに呼ぶ。discord_react と一緒に呼んでもよい。"
}
func (skipResponseTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string","description":"スキップ理由（ログ用）"}},"required":[]}`)
}
func (skipResponseTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return tool.StopResult("skipped"), nil
}

// containsSkipTool は ToolCall に skip_response が含まれているかを返す。
func containsSkipTool(calls []providers.ToolCall) bool {
	for _, tc := range calls {
		if tc.Function.Name == "skip_response" {
			return true
		}
	}
	return false
}

// execSideEffectsOnSkip は skip_response 以外のツール（discord_react 等）を実行する。
func execSideEffectsOnSkip(ctx context.Context, toolMap map[string]tool.Tool, calls []providers.ToolCall, logger *slog.Logger) {
	for _, tc := range calls {
		if tc.Function.Name == "skip_response" {
			continue
		}
		if t, ok := toolMap[tc.Function.Name]; ok {
			if _, err := t.Execute(ctx, []byte(tc.Function.Arguments)); err != nil {
				logger.Warn("skip中のツール失敗", "tool", tc.Function.Name, "error", err)
			}
		}
	}
}
