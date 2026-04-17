package vision

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
)

type servoTool struct {
	dev     deviceCommander
	tracker *ObjectTracker
}

func newServoTool(dev deviceCommander, tracker *ObjectTracker) tool.Tool {
	return &servoTool{dev: dev, tracker: tracker}
}

func (t *servoTool) Name() string        { return "body_turn_head" }
func (t *servoTool) ReadOnly() bool      { return false }
func (t *servoTool) Description() string { return "首を動かす。pan=左右(0-180,90が正面), tilt=上下(0-180,90が正面)" }
func (t *servoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pan":  {"type": "integer", "description": "左右角度 (0-180, 90=正面)", "minimum": 0, "maximum": 180},
			"tilt": {"type": "integer", "description": "上下角度 (0-180, 90=正面)", "minimum": 0, "maximum": 180}
		},
		"required": ["pan", "tilt"]
	}`)
}

func (t *servoTool) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args struct {
		Pan  int `json:"pan"`
		Tilt int `json:"tilt"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.ErrorResult("パラメータ解析失敗: " + err.Error()), nil
	}

	if !t.dev.IsDeviceConnected() {
		return tool.ErrorResult("デバイス未接続"), nil
	}

	if err := t.dev.SendDeviceCommand(map[string]any{
		"cmd":  "servo",
		"pan":  args.Pan,
		"tilt": args.Tilt,
	}); err != nil {
		return tool.ErrorResult("サーボコマンド送信失敗: " + err.Error()), nil
	}

	if t.tracker != nil {
		t.tracker.UpdatePosition(float64(args.Pan), float64(args.Tilt))
	}

	return tool.TextResult(fmt.Sprintf("サーボを移動: pan=%d, tilt=%d", args.Pan, args.Tilt)), nil
}
