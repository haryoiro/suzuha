package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/port/tool"
)

// deviceCommander sends commands to the physical device.
// Defined here (consumer-side) to avoid importing the device package.
type deviceCommander interface {
	SendDeviceCommand(cmd map[string]any) error
	BroadcastCommand(cmd map[string]any) error
	IsDeviceConnected() bool
}

// VisionDescriber is the interface for describing images via VLM.
type VisionDescriber interface {
	HasVisionCapability() (available bool, inline bool)
	DescribeImage(ctx context.Context, imageURL string, prompt ...string) (string, error)
}

// --- servo tool ---

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

// --- capture tool ---

type captureTool struct {
	dev deviceCommander
}

func newCaptureTool(dev deviceCommander) tool.Tool {
	return &captureTool{dev: dev}
}

func (t *captureTool) Name() string        { return "body_blink" }
func (t *captureTool) ReadOnly() bool      { return false }
func (t *captureTool) Description() string { return "まばたきして視界のスナップショットを保存する。" }
func (t *captureTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *captureTool) Execute(_ context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	if !t.dev.IsDeviceConnected() {
		return tool.ErrorResult("デバイス未接続"), nil
	}

	if err := t.dev.SendDeviceCommand(map[string]any{"cmd": "capture"}); err != nil {
		return tool.ErrorResult("キャプチャコマンド送信失敗: " + err.Error()), nil
	}

	return tool.TextResult("カメラにキャプチャを要求しました。画像は次のメッセージで届きます。"), nil
}

// --- face tool ---

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

// --- look tool ---

type lookTool struct {
	dev    deviceCommander
	frames *FrameStore
	vision VisionDescriber
}

func newLookTool(dev deviceCommander, frames *FrameStore, vision VisionDescriber) tool.Tool {
	return &lookTool{dev: dev, frames: frames, vision: vision}
}

func (t *lookTool) Name() string    { return "body_look" }
func (t *lookTool) ReadOnly() bool { return true }
func (t *lookTool) Description() string {
	return "自分の目で見る。今この瞬間、視界に何が映っているかを認識する。"
}
func (t *lookTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *lookTool) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	if !t.dev.IsDeviceConnected() {
		return tool.ErrorResult("カメラフレームなし（デバイス未接続）"), nil
	}

	if err := t.dev.SendDeviceCommand(map[string]any{"cmd": "capture"}); err != nil {
		slog.Warn("body_look: capture command failed", "error", err)
	}
	frame := t.frames.WaitForNewFrame(2 * time.Second)
	if frame == nil {
		frame = t.frames.LatestFrame()
	}
	if frame == nil {
		return tool.ErrorResult("カメラフレームなし"), nil
	}

	dataURI := fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(frame))

	if t.vision == nil {
		return tool.ErrorResult("今は目が見えない"), nil
	}

	available, inline := t.vision.HasVisionCapability()
	if !available {
		return tool.ErrorResult("今は目が見えない"), nil
	}

	if inline {
		result := tool.TextResult("[今の視界]")
		result.ImageURLs = []string{dataURI}
		return result, nil
	}

	desc, err := t.vision.DescribeImage(ctx, dataURI)
	if err != nil {
		return tool.ErrorResult("うまく見えなかった"), nil
	}

	return tool.TextResult(fmt.Sprintf("[今の視界]\n%s", desc)), nil
}
