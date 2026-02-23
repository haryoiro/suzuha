package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// MentionOptIn allows users to toggle whether the bot can mention them proactively.
type MentionOptIn struct {
	db *sql.DB
}

// NewMentionOptIn creates a new MentionOptIn tool.
func NewMentionOptIn(db *sql.DB) *MentionOptIn {
	return &MentionOptIn{db: db}
}

func (m *MentionOptIn) Name() string { return "mention_opt_in" }

func (m *MentionOptIn) Description() string {
	return `ユーザーのメンション許可設定を切り替えます。ユーザーが「メンションしていいよ」「勝手に話しかけていいよ」等と言った場合はopt_in=true、「メンションしないで」等の場合はopt_in=falseで呼び出してください。user_idとplatformはメッセージメタデータから取得してください。`
}

func (m *MentionOptIn) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id":  {"type": "string", "description": "The platform user ID (from message metadata)."},
			"platform": {"type": "string", "description": "The platform name (discord, cli)."},
			"opt_in":   {"type": "boolean", "description": "true to allow proactive mentions, false to disallow."}
		},
		"required": ["user_id", "platform", "opt_in"]
	}`)
}

type mentionOptInInput struct {
	UserID   string `json:"user_id"`
	Platform string `json:"platform"`
	OptIn    bool   `json:"opt_in"`
}

func (m *MentionOptIn) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in mentionOptInInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.UserID == "" || in.Platform == "" {
		return tool.ErrorResult("user_id and platform are required"), nil
	}

	// Resolve internal user ID from platform link.
	var internalID string
	err := m.db.QueryRowContext(ctx,
		`SELECT user_id FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
		in.Platform, in.UserID,
	).Scan(&internalID)
	if err != nil {
		return tool.ErrorResult("user not found"), nil
	}

	// Update metadata with mention_opt_in flag.
	optInVal := 0
	if in.OptIn {
		optInVal = 1
	}
	_, err = m.db.ExecContext(ctx,
		`UPDATE users
		 SET metadata = json_set(COALESCE(metadata, '{}'), '$.mention_opt_in', ?),
		     updated_at = ?
		 WHERE id = ?`,
		optInVal, time.Now(), internalID,
	)
	if err != nil {
		return nil, fmt.Errorf("mention_opt_in: update: %w", err)
	}

	if in.OptIn {
		return tool.TextResult("メンション許可を有効にしました。定期的に話題を提供する際にメンションします。"), nil
	}
	return tool.TextResult("メンション許可を無効にしました。今後はメンションしません。"), nil
}
