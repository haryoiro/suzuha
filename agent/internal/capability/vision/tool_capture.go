package vision

import (
	"context"
	"encoding/json"

	"github.com/haryoiro/suzuha/internal/port/tool"
)

type captureTool struct {
	dev deviceCommander
}

func newCaptureTool(dev deviceCommander) tool.Tool {
	return &captureTool{dev: dev}
}

func (t *captureTool) Name() string   { return "body_blink" }
func (t *captureTool) ReadOnly() bool { return false }
func (t *captureTool) Description() string {
	return "まばたきして視界のスナップショットを保存する。"
}
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
