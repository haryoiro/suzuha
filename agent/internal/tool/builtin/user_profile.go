package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
)

// ContextUpdater is called after a user profile is updated to keep short-term memory consistent.
type ContextUpdater func(userID, newName string)

// UpdateUserProfile allows the LLM to update a user's display name.
type UpdateUserProfile struct {
	users     user.Store
	onUpdate  ContextUpdater
}

// NewUpdateUserProfile creates the tool with a user store and an optional callback
// for updating short-term memory (agent context).
func NewUpdateUserProfile(users user.Store, onUpdate ContextUpdater) *UpdateUserProfile {
	return &UpdateUserProfile{users: users, onUpdate: onUpdate}
}

func (u *UpdateUserProfile) Name() string    { return "update_user_profile" }
func (u *UpdateUserProfile) ReadOnly() bool { return false }

func (u *UpdateUserProfile) Description() string {
	return `ユーザーの表示名（ニックネーム）を変更する。「〇〇と呼んで」と言われたときに使う。メッセージのuser_idとplatformの値を使うこと。`
}

func (u *UpdateUserProfile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id": {"type": "string", "description": "The platform user ID (from message metadata)."},
			"platform": {"type": "string", "description": "The platform name (discord, cli)."},
			"display_name": {"type": "string", "description": "The new display name / nickname to use."}
		},
		"required": ["user_id", "platform", "display_name"]
	}`)
}

type updateProfileInput struct {
	UserID      string `json:"user_id"`
	Platform    string `json:"platform"`
	DisplayName string `json:"display_name"`
}

func (u *UpdateUserProfile) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in updateProfileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.UserID == "" || in.DisplayName == "" {
		return tool.ErrorResult("user_idとdisplay_nameは必須です"), nil
	}

	// Resolve the user (auto-creates if not exists).
	resolved, err := u.users.Resolve(ctx, in.Platform, in.UserID, "")
	if err != nil {
		return nil, fmt.Errorf("update_user_profile: ユーザー解決に失敗: %w", err)
	}

	// Update display name in DB.
	if err := u.users.UpdateDisplayName(ctx, resolved.ID, in.DisplayName); err != nil {
		return nil, fmt.Errorf("update_user_profile: 更新に失敗: %w", err)
	}

	// Update short-term memory if callback is set.
	if u.onUpdate != nil {
		u.onUpdate(in.UserID, in.DisplayName)
	}

	return tool.TextResult(fmt.Sprintf("ユーザーの表示名を %q に更新しました。", in.DisplayName)), nil
}
