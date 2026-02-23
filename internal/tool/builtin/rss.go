package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler/tasks"
	"github.com/haryoiro/suzuha/internal/tool"
)

// --- rss_subscribe ---

// RSSSubscribe registers an RSS feed.
type RSSSubscribe struct {
	db *sql.DB
}

func NewRSSSubscribe(db *sql.DB) *RSSSubscribe {
	return &RSSSubscribe{db: db}
}

func (r *RSSSubscribe) Name() string { return "rss_subscribe" }

func (r *RSSSubscribe) Description() string {
	return `Register an RSS/Atom feed for monitoring. New articles will be checked periodically and interesting ones will be shared in the channel. Use the channel_id from the message metadata.`
}

func (r *RSSSubscribe) InputSchema() json.RawMessage {
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

func (r *RSSSubscribe) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssSubscribeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.URL == "" || in.ChannelID == "" {
		return tool.ErrorResult("url and channel_id are required"), nil
	}
	if in.Name == "" {
		in.Name = in.URL
	}

	store := tasks.NewFeedStore(r.db)
	feed := &tasks.Feed{
		Name:      in.Name,
		URL:       in.URL,
		ChannelID: in.ChannelID,
		CreatedBy: in.UserID,
	}

	if err := store.AddFeed(ctx, feed); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return tool.ErrorResult("this feed URL is already registered"), nil
		}
		return nil, fmt.Errorf("rss_subscribe: %w", err)
	}

	return tool.TextResult(fmt.Sprintf("Registered RSS feed %q (%s). New articles will be shared in this channel.", in.Name, in.URL)), nil
}

var _ tool.Tool = (*RSSSubscribe)(nil)

// --- rss_unsubscribe ---

// RSSUnsubscribe removes an RSS feed registration.
type RSSUnsubscribe struct {
	db *sql.DB
}

func NewRSSUnsubscribe(db *sql.DB) *RSSUnsubscribe {
	return &RSSUnsubscribe{db: db}
}

func (r *RSSUnsubscribe) Name() string { return "rss_unsubscribe" }

func (r *RSSUnsubscribe) Description() string {
	return `Remove an RSS/Atom feed registration. Accepts a feed name, URL, or ID.`
}

func (r *RSSUnsubscribe) InputSchema() json.RawMessage {
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

func (r *RSSUnsubscribe) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssUnsubscribeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.Identifier == "" {
		return tool.ErrorResult("identifier is required"), nil
	}

	store := tasks.NewFeedStore(r.db)
	if err := store.RemoveFeed(ctx, in.Identifier); err != nil {
		return tool.ErrorResult(fmt.Sprintf("Could not remove feed: %v", err)), nil
	}

	return tool.TextResult(fmt.Sprintf("Removed RSS feed %q.", in.Identifier)), nil
}

var _ tool.Tool = (*RSSUnsubscribe)(nil)

// --- rss_list ---

// RSSList shows all registered RSS feeds.
type RSSList struct {
	db *sql.DB
}

func NewRSSList(db *sql.DB) *RSSList {
	return &RSSList{db: db}
}

func (r *RSSList) Name() string { return "rss_list" }

func (r *RSSList) Description() string {
	return `List all registered RSS/Atom feeds with their status.`
}

func (r *RSSList) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (r *RSSList) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	store := tasks.NewFeedStore(r.db)
	feeds, err := store.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("rss_list: %w", err)
	}

	if len(feeds) == 0 {
		return tool.TextResult("No RSS feeds registered."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Registered feeds (%d):\n", len(feeds))
	for _, f := range feeds {
		status := "enabled"
		if !f.Enabled {
			status = "disabled"
		}
		lastPolled := "never"
		if f.LastPolled != nil {
			lastPolled = f.LastPolled.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&sb, "- %s (%s) [%s] last_polled=%s channel=%s\n",
			f.Name, f.URL, status, lastPolled, f.ChannelID)
	}

	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*RSSList)(nil)

// --- rss_preference ---

// RSSPreference saves a user's RSS notification preference.
type RSSPreference struct {
	memStore memory.Store
}

func NewRSSPreference(memStore memory.Store) *RSSPreference {
	return &RSSPreference{memStore: memStore}
}

func (r *RSSPreference) Name() string { return "rss_preference" }

func (r *RSSPreference) Description() string {
	return `Save a user's RSS notification preference (e.g. exclusions, interests). The preference is stored as a user memory and will be considered when scoring articles. Examples: "言語のチェンジログはいらない", "英語の記事だけ送って", "初心者向けの記事がいい"`
}

func (r *RSSPreference) InputSchema() json.RawMessage {
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

func (r *RSSPreference) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in rssPreferenceInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.UserID == "" || in.Preference == "" {
		return tool.ErrorResult("user_id and preference are required"), nil
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

	return tool.TextResult(fmt.Sprintf("Saved RSS preference: %q", in.Preference)), nil
}

var _ tool.Tool = (*RSSPreference)(nil)
