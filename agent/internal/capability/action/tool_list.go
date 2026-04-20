package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/haryoiro/suzuha/internal/port/tool"
)

// ListTool は予約済みメッセージの一覧を表示するツール。
type ListTool struct {
	store *Store
}

// NewListTool は ListTool のインスタンスを生成する。
func NewListTool(store *Store) *ListTool {
	return &ListTool{store: store}
}

func (t *ListTool) Name() string   { return "schedule_list" }
func (t *ListTool) ReadOnly() bool { return true }

func (t *ListTool) Description() string {
	return "予約済みのメッセージ一覧を見る。ユーザーIDで絞り込みもできる。"
}

func (t *ListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"created_by": {"type": "string", "description": "Optional: filter by creator user ID."}
		}
	}`)
}

type listInput struct {
	CreatedBy string `json:"created_by"`
}

func (t *ListTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in listInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return tool.ErrorResult("無効な入力: " + err.Error()), nil
		}
	}

	var actions []Action
	var err error
	if in.CreatedBy != "" {
		actions, err = t.store.ListPendingByCreator(ctx, in.CreatedBy)
	} else {
		actions, err = t.store.ListPending(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("schedule_list: %w", err)
	}

	if len(actions) == 0 {
		return tool.TextResult("保留中のスケジュールはありません。"), nil
	}

	var sb strings.Builder
	for _, a := range actions {
		preview := a.Content
		if utf8.RuneCountInString(preview) > 50 {
			preview = string([]rune(preview)[:50]) + "…"
		}
		fmt.Fprintf(&sb, "- ID: %s | %s | ch: %s | %q", a.ID, a.ScheduledAt.Format("2006-01-02 15:04 MST"), a.ChannelID, preview)
		if a.CronExpr != "" {
			fmt.Fprintf(&sb, " | recurring: %s", a.CronExpr)
		}
		sb.WriteByte('\n')
	}
	return tool.TextResult(sb.String()), nil
}
