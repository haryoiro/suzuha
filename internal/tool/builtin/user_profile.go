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

func (u *UpdateUserProfile) Name() string { return "update_user_profile" }

func (u *UpdateUserProfile) Description() string {
	return `Update a user's display name (nickname). Use this when a user asks to be called by a specific name (e.g. "〇〇と呼んで", "call me X"). Use the user_id and platform values from the message metadata (e.g. user_id=646795450577453058 platform=discord).`
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
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.UserID == "" || in.DisplayName == "" {
		return tool.ErrorResult("user_id and display_name are required"), nil
	}

	// Resolve the user (auto-creates if not exists).
	resolved, err := u.users.Resolve(ctx, in.Platform, in.UserID, "")
	if err != nil {
		return nil, fmt.Errorf("update_user_profile: resolve: %w", err)
	}

	// Update display name in DB.
	if err := u.users.UpdateDisplayName(ctx, resolved.ID, in.DisplayName); err != nil {
		return nil, fmt.Errorf("update_user_profile: update: %w", err)
	}

	// Update short-term memory if callback is set.
	if u.onUpdate != nil {
		u.onUpdate(in.UserID, in.DisplayName)
	}

	return tool.TextResult(fmt.Sprintf("Updated display name for user to %q.", in.DisplayName)), nil
}
