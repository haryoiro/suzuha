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

type lookTool struct {
	dev    deviceCommander
	frames *FrameStore
	vision VisionDescriber
}

func newLookTool(dev deviceCommander, frames *FrameStore, vision VisionDescriber) tool.Tool {
	return &lookTool{dev: dev, frames: frames, vision: vision}
}

func (t *lookTool) Name() string   { return "body_look" }
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
