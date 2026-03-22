package device

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// VisionDescriber is the interface for describing images via VLM.
type VisionDescriber interface {
	HasVision() bool
	IsVisionCapable() bool
	DescribeImage(ctx context.Context, imageURL string) (string, error)
}

// NewServoTool creates a tool for controlling the device's pan/tilt servos.
func NewServoTool(hub *Hub) tool.Tool {
	return &servoTool{hub: hub}
}

type servoTool struct{ hub *Hub }

func (t *servoTool) Name() string        { return "body_turn_head" }
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

	dev := t.hub.Device()
	if dev == nil {
		return tool.ErrorResult("デバイス未接続"), nil
	}

	if err := dev.SendCommand(map[string]any{
		"cmd":  "servo",
		"pan":  args.Pan,
		"tilt": args.Tilt,
	}); err != nil {
		return tool.ErrorResult("サーボコマンド送信失敗: " + err.Error()), nil
	}

	return tool.TextResult(fmt.Sprintf("サーボを移動: pan=%d, tilt=%d", args.Pan, args.Tilt)), nil
}

// NewCaptureTool creates a tool for requesting an image capture from the device.
func NewCaptureTool(hub *Hub) tool.Tool {
	return &captureTool{hub: hub}
}

type captureTool struct{ hub *Hub }

func (t *captureTool) Name() string        { return "body_blink" }
func (t *captureTool) Description() string { return "まばたきして視界のスナップショットを保存する。" }
func (t *captureTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *captureTool) Execute(_ context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	dev := t.hub.Device()
	if dev == nil {
		return tool.ErrorResult("デバイス未接続"), nil
	}

	if err := dev.SendCommand(map[string]any{"cmd": "capture"}); err != nil {
		return tool.ErrorResult("キャプチャコマンド送信失敗: " + err.Error()), nil
	}

	return tool.TextResult("カメラにキャプチャを要求しました。画像は次のメッセージで届きます。"), nil
}

// NewFaceTool creates a tool for setting the device's facial expression.
func NewFaceTool(hub *Hub) tool.Tool {
	return &faceTool{hub: hub}
}

type faceTool struct{ hub *Hub }

func (t *faceTool) Name() string        { return "body_expression" }
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

	// Broadcast expression to all connected clients (ESP + Web).
	if err := t.hub.BroadcastCommand(map[string]any{
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

// NewLookTool creates a tool that lets the LLM "see" the latest camera frame.
// It reads the latest JPEG from FrameStore, sends it to the VLM, and returns
// the description as a tool result. Does NOT publish to the event bus.
func NewLookTool(hub *Hub, vision VisionDescriber) tool.Tool {
	return &lookTool{hub: hub, vision: vision}
}

type lookTool struct {
	hub    *Hub
	vision VisionDescriber
}

func (t *lookTool) Name() string { return "body_look" }
func (t *lookTool) Description() string {
	return "自分の目で見る。今この瞬間、視界に何が映っているかを認識する。"
}
func (t *lookTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *lookTool) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	dev := t.hub.Device()
	if dev == nil {
		return tool.ErrorResult("カメラフレームなし（デバイス未接続）"), nil
	}

	// Request a fresh capture and wait for the new frame.
	_ = dev.SendCommand(map[string]any{"cmd": "capture"})
	frame := t.hub.frames.WaitForNewFrame(2 * time.Second)
	if frame == nil {
		// Fall back to cached frame.
		frame = t.hub.frames.LatestFrame()
	}
	if frame == nil {
		return tool.ErrorResult("カメラフレームなし"), nil
	}

	dataURI := fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(frame))

	// If the active LLM supports vision natively, return the image directly.
	if t.vision != nil && t.vision.IsVisionCapable() {
		result := tool.TextResult("[今の視界]")
		result.ImageURLs = []string{dataURI}
		return result, nil
	}

	// Otherwise, fall back to separate VLM for text description.
	if t.vision == nil || !t.vision.HasVision() {
		return tool.ErrorResult("今は目が見えない"), nil
	}

	desc, err := t.vision.DescribeImage(ctx, dataURI)
	if err != nil {
		return tool.ErrorResult("うまく見えなかった"), nil
	}

	return tool.TextResult(fmt.Sprintf("[今の視界]\n%s", desc)), nil
}
