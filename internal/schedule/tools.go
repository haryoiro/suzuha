package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
Use the channel_id from the message metadata. The scheduled_at must be in RFC3339 format (e.g. "2025-01-15T10:00:00+09:00").
For recurring schedules, provide a cron_expr in standard 5-field cron format (minute hour day month weekday).
Examples: "0 9 * * *" = every day at 9:00, "0 9 * * 1-5" = weekdays at 9:00, "*/30 * * * *" = every 30 minutes.`
}

func (t *CreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id":   {"type": "string", "description": "Target channel ID (from message metadata)."},
			"content":      {"type": "string", "description": "Message content to send (max 2000 chars)."},
			"scheduled_at": {"type": "string", "description": "When to send, in RFC3339 format (e.g. 2025-01-15T10:00:00+09:00)."},
			"cron_expr":    {"type": "string", "description": "Optional: 5-field cron expression for recurring schedule. If set, scheduled_at is the first occurrence."},
			"created_by":   {"type": "string", "description": "Optional: user ID who requested this (from message metadata)."}
		},
		"required": ["channel_id", "content", "scheduled_at"]
	}`)
}

type createInput struct {
	ChannelID   string `json:"channel_id"`
	Content     string `json:"content"`
	ScheduledAt string `json:"scheduled_at"`
	CronExpr    string `json:"cron_expr"`
	CreatedBy   string `json:"created_by"`
}

func (t *CreateTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}

	if in.ChannelID == "" {
		return tool.ErrorResult("channel_id is required"), nil
	}
	if strings.TrimSpace(in.Content) == "" {
		return tool.ErrorResult("content is required"), nil
	}
	if utf8.RuneCountInString(in.Content) > 2000 {
		return tool.ErrorResult("content must be 2000 characters or less"), nil
	}

	scheduledAt, err := time.Parse(time.RFC3339, in.ScheduledAt)
	if err != nil {
		return tool.ErrorResult("scheduled_at must be RFC3339 format (e.g. 2025-01-15T10:00:00+09:00): " + err.Error()), nil
	}
	if time.Until(scheduledAt) < time.Minute {
		return tool.ErrorResult("scheduled_at must be at least 1 minute in the future"), nil
	}

	// Validate cron expression if provided.
	if in.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, parseErr := parser.Parse(in.CronExpr); parseErr != nil {
			return tool.ErrorResult("invalid cron_expr: " + parseErr.Error()), nil
		}
	}

	action := &Action{
		ChannelID:   in.ChannelID,
		Content:     in.Content,
		ScheduledAt: scheduledAt,
		CronExpr:    in.CronExpr,
		CreatedBy:   in.CreatedBy,
	}
	if err := t.store.Create(ctx, action); err != nil {
		return nil, fmt.Errorf("schedule_create: %w", err)
	}

	result := fmt.Sprintf("Scheduled (ID: %s) at %s", action.ID, scheduledAt.Format("2006-01-02 15:04 MST"))
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
		return tool.TextResult("No pending schedules."), nil
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
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.ID == "" {
		return tool.ErrorResult("id is required"), nil
	}

	ok, err := t.store.Cancel(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("schedule_cancel: %w", err)
	}
	if !ok {
		return tool.ErrorResult("schedule not found or already executed/cancelled"), nil
	}
	return tool.TextResult(fmt.Sprintf("Cancelled schedule %s", in.ID)), nil
}
