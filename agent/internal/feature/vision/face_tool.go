package vision

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
)

type faceTool struct {
	dev deviceCommander
}

func newFaceTool(dev deviceCommander) tool.Tool {
	return &faceTool{dev: dev}
}

func (t *faceTool) Name() string        { return "body_expression" }
func (t *faceTool) ReadOnly() bool      { return false }
func (t *faceTool) Description() string { return "自分の表情を変える。0=通常, 1=嬉しい, 2=悲しい, 3=驚き, 4=怒り, 5=眠い, 6=考え中, 7=喋り中" }
func (t *faceTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"expression": {"type": "integer", "description": "表情ID (0-7)", "minimum": 0, "maximum": 7}
		},
		"required": ["expression"]
	}`)
}

func (t *faceTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args struct {
		Expression int `json:"expression"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.ErrorResult("パラメータ解析失敗: " + err.Error()), nil
	}

	if err := t.dev.BroadcastCommand(map[string]any{
		"cmd":        "face",
		"expression": args.Expression,
	}); err != nil {
		return tool.ErrorResult("表情コマンド送信失敗: " + err.Error()), nil
	}

	names := []string{"通常", "嬉しい", "悲しい", "驚き", "怒り", "眠い", "考え中", "喋り中"}
	name := "不明"
	if args.Expression >= 0 && args.Expression < len(names) {
		name = names[args.Expression]
	}
	return tool.TextResult(fmt.Sprintf("表情を変更: %s", name)), nil
}
