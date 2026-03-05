package rss

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
)

// --- rss_subscribe ---

// SubscribeTool registers an RSS feed.
type SubscribeTool struct {
	db *sql.DB
}

func NewSubscribeTool(db *sql.DB) *SubscribeTool {
	return &SubscribeTool{db: db}
}

func (r *SubscribeTool) Name() string { return "rss_subscribe" }

func (r *SubscribeTool) Description() string {
	return `Register an RSS/Atom feed for monitoring. New articles will be checked periodically and interesting ones will be shared in the channel. Use the channel_id from the message metadata.`
}

func (r *SubscribeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url":        {"type": "string", "description": "The RSS/Atom feed URL."},
			"name":       {"type": "string", "description": "A short name for the feed (optional, defaults to URL)."},
			"channel_id": {"type": "string", "description": "The channel ID where notifications will be sent (from message metadata)."},
			"user_id":    {"type": "string", "description": "The platform user ID of the requester (from message metadata)."}
		},
		"required": ["url", "channel_id"]
	}`)
}

type rssSubscribeInput struct {
	URL       string `json:"url"`
	Name      string `json:"name"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
}

func (r *SubscribeTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssSubscribeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.URL == "" || in.ChannelID == "" {
		return tool.ErrorResult("url と channel_id は必須です"), nil
	}
	if in.Name == "" {
		in.Name = in.URL
	}

	store := NewFeedStore(r.db)
	feed := &Feed{
		Name:      in.Name,
		URL:       in.URL,
		ChannelID: in.ChannelID,
		CreatedBy: in.UserID,
	}

	if err := store.AddFeed(ctx, feed); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return tool.ErrorResult("このフィードURLは既に登録されています"), nil
		}
		return nil, fmt.Errorf("rss_subscribe: %w", err)
	}

	return tool.TextResult(fmt.Sprintf("RSSフィード %q (%s) を登録しました。新着記事はこのチャンネルに共有されます。", in.Name, in.URL)), nil
}

var _ tool.Tool = (*SubscribeTool)(nil)

// --- rss_unsubscribe ---

// UnsubscribeTool removes an RSS feed registration.
type UnsubscribeTool struct {
	db *sql.DB
}

func NewUnsubscribeTool(db *sql.DB) *UnsubscribeTool {
	return &UnsubscribeTool{db: db}
}

func (r *UnsubscribeTool) Name() string { return "rss_unsubscribe" }

func (r *UnsubscribeTool) Description() string {
	return `Remove an RSS/Atom feed registration. Accepts a feed name, URL, or ID.`
}

func (r *UnsubscribeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"identifier": {"type": "string", "description": "The feed name, URL, or ID to remove."}
		},
		"required": ["identifier"]
	}`)
}

type rssUnsubscribeInput struct {
	Identifier string `json:"identifier"`
}

func (r *UnsubscribeTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssUnsubscribeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Identifier == "" {
		return tool.ErrorResult("identifier は必須です"), nil
	}

	store := NewFeedStore(r.db)
	if err := store.RemoveFeed(ctx, in.Identifier); err != nil {
		return tool.ErrorResult(fmt.Sprintf("フィードを削除できませんでした: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("RSSフィード %q を削除しました。", in.Identifier)), nil
}

var _ tool.Tool = (*UnsubscribeTool)(nil)

// --- rss_list ---

// ListTool shows all registered RSS feeds.
type ListTool struct {
	db *sql.DB
}

func NewListTool(db *sql.DB) *ListTool {
	return &ListTool{db: db}
}

func (r *ListTool) Name() string { return "rss_list" }

func (r *ListTool) Description() string {
	return `List all registered RSS/Atom feeds with their status.`
}

func (r *ListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (r *ListTool) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	store := NewFeedStore(r.db)
	feeds, err := store.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("rss_list: %w", err)
	}

	if len(feeds) == 0 {
		return tool.TextResult("登録されているRSSフィードはありません。"), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "登録フィード (%d件):\n", len(feeds))
	for _, f := range feeds {
		status := "有効"
		if !f.Enabled {
			status = "無効"
		}
		lastPolled := "未取得"
		if f.LastPolled != nil {
			lastPolled = f.LastPolled.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&sb, "- %s (%s) [%s] last_polled=%s channel=%s\n",
			f.Name, f.URL, status, lastPolled, f.ChannelID)
	}

	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*ListTool)(nil)

// --- rss_preference ---

// PreferenceTool saves a user's RSS notification preference.
type PreferenceTool struct {
	memStore memory.Store
}

func NewPreferenceTool(memStore memory.Store) *PreferenceTool {
	return &PreferenceTool{memStore: memStore}
}

func (r *PreferenceTool) Name() string { return "rss_preference" }

func (r *PreferenceTool) Description() string {
	return `Save a user's RSS notification preference (e.g. exclusions, interests). The preference is stored as a user memory and will be considered when scoring articles. Examples: "言語のチェンジログはいらない", "英語の記事だけ送って", "初心者向けの記事がいい"`
}

func (r *PreferenceTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id":    {"type": "string", "description": "The platform user ID (from message metadata)."},
			"preference": {"type": "string", "description": "The preference in natural language."}
		},
		"required": ["user_id", "preference"]
	}`)
}

type rssPreferenceInput struct {
	UserID     string `json:"user_id"`
	Preference string `json:"preference"`
}

func (r *PreferenceTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssPreferenceInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.UserID == "" || in.Preference == "" {
		return tool.ErrorResult("user_id と preference は必須です"), nil
	}

	mem := &memory.Memory{
		Type:    memory.MemoryTypeUser,
		Content: in.Preference,
		Metadata: map[string]any{
			"user_id":        in.UserID,
			"rss_preference": true,
		},
	}

	if err := r.memStore.Save(ctx, mem); err != nil {
		return nil, fmt.Errorf("rss_preference: save: %w", err)
	}

	return tool.TextResult(fmt.Sprintf("RSS設定を保存しました: %q", in.Preference)), nil
}

var _ tool.Tool = (*PreferenceTool)(nil)
