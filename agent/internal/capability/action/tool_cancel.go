package action

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/port/tool"
)

// CancelTool は予約済みメッセージをキャンセルするツール。
type CancelTool struct {
	store *Store
}

// NewCancelTool は CancelTool のインスタンスを生成する。
func NewCancelTool(store *Store) *CancelTool {
	return &CancelTool{store: store}
}

func (t *CancelTool) Name() string   { return "schedule_cancel" }
func (t *CancelTool) ReadOnly() bool { return false }

func (t *CancelTool) Description() string {
	return "予約済みのメッセージをIDで取り消す。繰り返し予約も止まる。"
}

func (t *CancelTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "The schedule ID to cancel."}
		},
		"required": ["id"]
	}`)
}

type cancelInput struct {
	ID string `json:"id"`
}

func (t *CancelTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in cancelInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.ID == "" {
		return tool.ErrorResult("id は必須です"), nil
	}

	ok, err := t.store.Cancel(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("schedule_cancel: %w", err)
	}
	if !ok {
		return tool.ErrorResult("スケジュールが見つからないか、既に実行済み・キャンセル済みです"), nil
	}
	return tool.TextResult(fmt.Sprintf("スケジュール %s をキャンセルしました", in.ID)), nil
}
