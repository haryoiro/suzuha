package action

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/robfig/cron/v3"
)

// --- schedule_create ---

type CreateTool struct {
	store *Store
}

func NewCreateTool(store *Store) *CreateTool {
	return &CreateTool{store: store}
}

func (t *CreateTool) Name() string { return "schedule_create" }

func (t *CreateTool) Description() string {
	return `Schedule a message to be sent to a Discord channel at a specified time.
Use the channel_id from the message metadata.
For recurring schedules, provide a cron_expr in standard 5-field cron format (minute hour day month weekday).
Times and cron expressions are interpreted in the configured timezone (see config.yaml timezone setting).
If scheduled_at is omitted, the next occurrence is automatically calculated from cron_expr.
Use random_minutes to add a random offset (0 to N minutes) to each occurrence — useful for natural-feeling recurring messages.
Examples: cron_expr "0 8 * * *" with random_minutes 240 = every day at a random time between 8:00-12:00.
Set mode to "prompt" to treat content as an instruction — the LLM will generate a response from it before posting. Default mode is "direct" (post content as-is).`
}

func (t *CreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id":   {"type": "string", "description": "Target channel ID (from message metadata)."},
			"content":      {"type": "string", "description": "Message content to send (max 2000 chars)."},
			"scheduled_at": {"type": "string", "description": "Optional: when to send, in RFC3339 format (e.g. 2025-01-15T10:00:00+09:00). If omitted and cron_expr is set, the next cron occurrence is used automatically."},
			"cron_expr":      {"type": "string", "description": "Optional: 5-field cron expression for recurring schedule (e.g. '0 8 * * *' = daily at 8:00). If scheduled_at is omitted, the first occurrence is auto-calculated from the cron expression."},
			"random_minutes": {"type": "integer", "description": "Optional: add a random offset of 0 to N minutes to each scheduled time. E.g. cron '0 8 * * *' + random_minutes 240 = daily random between 8:00-12:00."},
			"created_by":     {"type": "string", "description": "Optional: user ID who requested this (from message metadata)."},
			"mode":           {"type": "string", "enum": ["direct", "prompt"], "description": "Optional: 'direct' posts content as-is (default). 'prompt' treats content as an instruction and generates a response via LLM before posting."}
		},
		"required": ["channel_id", "content"]
	}`)
}

type createInput struct {
	ChannelID     string `json:"channel_id"`
	Content       string `json:"content"`
	ScheduledAt   string `json:"scheduled_at"`
	CronExpr      string `json:"cron_expr"`
	RandomMinutes int    `json:"random_minutes"`
	CreatedBy     string `json:"created_by"`
	Mode          string `json:"mode"`
}

func (t *CreateTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	if in.ChannelID == "" {
		return tool.ErrorResult("channel_id は必須です"), nil
	}
	if strings.TrimSpace(in.Content) == "" {
		return tool.ErrorResult("content は必須です"), nil
	}
	if utf8.RuneCountInString(in.Content) > 2000 {
		return tool.ErrorResult("content は2000文字以下にしてください"), nil
	}

	// Validate cron expression if provided.
	var cronSchedule cron.Schedule
	if in.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		var parseErr error
		cronSchedule, parseErr = parser.Parse(in.CronExpr)
		if parseErr != nil {
			return tool.ErrorResult("無効な cron_expr: " + parseErr.Error()), nil
		}
	}

	var scheduledAt time.Time
	switch {
	case in.ScheduledAt != "":
		var err error
		scheduledAt, err = time.Parse(time.RFC3339, in.ScheduledAt)
		if err != nil {
			return tool.ErrorResult("scheduled_at はRFC3339形式で指定してください (例: 2025-01-15T10:00:00+09:00): " + err.Error()), nil
		}
		if time.Until(scheduledAt) < time.Minute {
			return tool.ErrorResult("scheduled_at は少なくとも1分後の時刻を指定してください"), nil
		}
	case cronSchedule != nil:
		// Auto-calculate the next occurrence from the cron expression.
		scheduledAt = cronSchedule.Next(jtime.Now())
	default:
		return tool.ErrorResult("scheduled_at または cron_expr のいずれかが必須です"), nil
	}

	// Apply random offset.
	if in.RandomMinutes > 0 {
		offset := time.Duration(rand.IntN(in.RandomMinutes)) * time.Minute
		scheduledAt = scheduledAt.Add(offset)
	}

	mode := in.Mode
	if mode == "" {
		mode = "direct"
	}
	action := &Action{
		ChannelID:     in.ChannelID,
		Content:       in.Content,
		Mode:          mode,
		ScheduledAt:   scheduledAt,
		CronExpr:      in.CronExpr,
		RandomMinutes: in.RandomMinutes,
		CreatedBy:     in.CreatedBy,
	}
	if err := t.store.Create(ctx, action); err != nil {
		return nil, fmt.Errorf("schedule_create: %w", err)
	}

	result := fmt.Sprintf("スケジュール登録完了 (ID: %s) 実行予定: %s", action.ID, scheduledAt.Format("2006-01-02 15:04 MST"))
	if in.CronExpr != "" {
		result += fmt.Sprintf(" (recurring: %s)", in.CronExpr)
	}
	return tool.TextResult(result), nil
}

// --- schedule_list ---

type ListTool struct {
	store *Store
}

func NewListTool(store *Store) *ListTool {
	return &ListTool{store: store}
}

func (t *ListTool) Name() string { return "schedule_list" }

func (t *ListTool) Description() string {
	return "List pending scheduled messages. Optionally filter by created_by user ID."
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
		_ = json.Unmarshal(input, &in)
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

// --- schedule_cancel ---

type CancelTool struct {
	store *Store
}

func NewCancelTool(store *Store) *CancelTool {
	return &CancelTool{store: store}
}

func (t *CancelTool) Name() string { return "schedule_cancel" }

func (t *CancelTool) Description() string {
	return "Cancel a pending scheduled message by ID. Also stops recurring schedules."
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
