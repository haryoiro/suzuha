package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

// Perceive ingests all events in the batch into context, resolving users,
// describing images, and bootstrapping channel history.
// Returns a Perception summarizing what was observed, or nil if all events
// were filtered out (e.g. disabled channels).
func (a *Agent) Perceive(ctx context.Context, batch []event.Event) *Perception {
	// Filter out disabled channels.
	if a.channelSettings != nil {
		var filtered []event.Event
		for _, evt := range batch {
			chID := evt.Message.Channel
			if chID != "" && !evt.Message.IsDM && a.channelSettings.GetMode(chID) == channelpkg.ModeDisabled {
				a.logger.Debug("無効なチャンネルをスキップ", "channel", chID)
				continue
			}
			filtered = append(filtered, evt)
		}
		if len(filtered) == 0 {
			return nil
		}
		batch = filtered
	}

	// Ingest all events into context.
	turnStartIdx := a.ctx.Len()
	var lastMsg llm.Message
	var lastEvt event.Event
	var userCloseness, userInterest float64
	var directlyAddressed bool
	for _, evt := range batch {
		msg, closeness, interest := a.ingestEvent(ctx, evt)
		lastMsg = msg
		lastEvt = evt
		userCloseness = closeness
		userInterest = interest
		if isDirectlyAddressed(evt, a.botID) {
			directlyAddressed = true
		}
	}

	channel := lastEvt.Message.Channel
	isDM := lastEvt.Message.IsDM

	_ = lastMsg // used via field access below
	return &Perception{
		LastMessage:       lastMsg,
		LastEvent:         lastEvt,
		Channel:           channel,
		IsDM:              isDM,
		IsVoice:           lastEvt.Message.IsVoice,
		DirectlyAddressed: directlyAddressed,
		SenderIsBot:       lastEvt.Message.IsBot,
		MaxCloseness:      userCloseness,
		MaxInterest:       userInterest,
		TurnStartIdx:      turnStartIdx,
	}
}

// ingestEvent processes a single event: resolves the user, adds the message
// to context, and injects channel history. It does NOT trigger LLM completion.
// Returns the message and the user's affinity scores.
func (a *Agent) ingestEvent(ctx context.Context, evt event.Event) (llm.Message, float64, float64) {
	msg := eventToMessage(evt)

	a.logger.Info("イベント受信",
		"source", evt.Source, "type", evt.Type,
		"user", msg.UserName, "user_id", msg.UserID,
		"channel", msg.Channel, "content", truncate(msg.Content, 100))

	// Resolve user identity (auto-create if not exists).
	var userCloseness, userInterest float64
	if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
		u, err := a.users.Resolve(ctx, msg.Source, msg.UserID, msg.UserName)
		if err != nil {
			a.logger.Warn("ユーザー解決失敗", "error", err)
		} else {
			if u.DisplayName != "" {
				msg.UserName = u.DisplayName
				a.logger.Debug("ユーザー解決済み", "display_name", u.DisplayName, "role", u.Role,
					"closeness", u.Closeness, "trust", u.Trust, "interest", u.Interest)
			}
			userCloseness = u.Closeness
			userInterest = u.Interest
			guildID := evt.Message.GuildID
			guildName := evt.Message.GuildName
			channelName := evt.Message.ChannelName
			if guildID != "" {
				if err := a.users.TrackGuildChannel(ctx, u.ID, guildID, guildName, msg.Channel, channelName); err != nil {
					a.logger.Warn("ギルドチャンネル追跡失敗", "error", err)
				}
			}
		}
	}

	// Track channel activity for topic backoff (non-bot, non-internal messages only).
	if msg.Channel != "" && a.db != nil && msg.UserID != a.botID && evt.Source != event.SourceInternal {
		_, _ = a.db.ExecContext(ctx,
			`INSERT INTO channel_activity (channel_id, last_user_message_at) VALUES (?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET last_user_message_at = excluded.last_user_message_at`,
			msg.Channel, time.Now())
	}

	// Describe attached images via VLM (if configured).
	if a.llm != nil && a.llm.HasVision() {
		if urls := extractImageURLs(evt); len(urls) > 0 {
			descriptions := a.describeImages(ctx, urls)
			if descriptions != "" {
				msg.Content += "\n" + descriptions
			}
		}
	}

	// Bootstrap channel history if this is a new channel.
	a.injectChannelHistory(ctx, msg.Channel, msg.Content, msg.Source)

	// Add to context (skip self_prompt — these are injected as ephemeral in Think).
	if evt.Type != event.TypeSelfPrompt {
		a.ctx.Add(msg)
	}

	return msg, userCloseness, userInterest
}

// eventToMessage converts an event to an llm.Message.
func eventToMessage(evt event.Event) llm.Message {
	m := evt.Message
	role := "user"
	if evt.Source == event.SourceInternal {
		role = "system"
	}
	return llm.Message{
		Role:        role,
		Content:     m.Content,
		UserID:      m.UserID,
		UserName:    m.UserName,
		Source:      evt.Source,
		Channel:     m.Channel,
		ChannelName: m.ChannelName,
		GuildID:     m.GuildID,
		GuildName:   m.GuildName,
		MessageID:   m.MessageID,
		Timestamp:   evt.Timestamp,
	}
}

// extractImageURLs pulls image URLs from an event's typed payload.
func extractImageURLs(evt event.Event) []string {
	return evt.Message.ImageURLs
}

// describeImages calls the VLM for each image URL and returns a combined description.
func (a *Agent) describeImages(ctx context.Context, urls []string) string {
	const maxImages = 4
	if len(urls) > maxImages {
		urls = urls[:maxImages]
	}

	var parts []string
	for i, u := range urls {
		desc, err := a.llm.DescribeImage(ctx, u)
		if err != nil {
			a.logger.Warn("ビジョン: 画像説明失敗", "url", u, "error", err)
			desc = "画像の読み取りに失敗しました"
		}
		parts = append(parts, fmt.Sprintf("[添付画像%d: %s]", i+1, desc))
	}
	return strings.Join(parts, "\n")
}

// injectChannelHistory fetches recent messages for a channel not yet seen
// in the context. Uses the discord_get_history tool if available,
// falling back to recent memory search.
func (a *Agent) injectChannelHistory(ctx context.Context, channelID, messageContent, source string) {
	if channelID == "" {
		return
	}
	if a.ctx.HasChannelHistory(channelID) {
		return
	}
	// Remove stale history (e.g. from DB restore) before injecting fresh one.
	a.ctx.RemoveChannelHistory(channelID)
	a.ctx.MarkChannelSeen(channelID)

	var content string
	if histTool, ok := a.tools.Get("discord_get_history"); ok {
		input, _ := json.Marshal(map[string]any{
			"channel_id": channelID,
			"limit":      10,
		})
		result, err := histTool.Execute(ctx, input)
		if err != nil {
			a.logger.Warn("チャンネル履歴ツール失敗", "channel", channelID, "error", err)
		} else if result != nil && !result.IsError && len(result.Content) > 0 && result.Content[0].Text != "" {
			content = a.formatChannelHistory(ctx, channelID, result.Content[0].Text, source)
		}
	}

	if content == "" && a.memory != nil {
		since := time.Now().Add(-3 * 24 * time.Hour)
		memories, err := a.memory.SearchRecent(ctx, messageContent, 5, since)
		if err != nil {
			a.logger.Debug("チャンネルメモリフォールバック失敗", "error", err)
		}
		if len(memories) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "[Recent related memories for channel=%s]\n", channelID)
			for _, m := range memories {
				fmt.Fprintf(&b, "- [%s] %s\n", m.Type, m.Content)
			}
			content = b.String()
		}
	}

	if content != "" {
		a.ctx.Add(llm.Message{
			Role:      "system",
			Content:   content,
			Timestamp: jtime.Now(),
		})
		a.logger.Info("チャンネル履歴を注入", "channel", channelID, "length", len(content))
	}
}

// formatChannelHistory parses the history tool's JSON output and resolves
// author names via the user store.
func (a *Agent) formatChannelHistory(ctx context.Context, channelID, rawJSON, source string) string {
	type histMsg struct {
		AuthorID string `json:"author_id"`
		Author   string `json:"author"`
		Content  string `json:"content"`
		Time     string `json:"time"`
	}
	var msgs []histMsg
	if err := json.Unmarshal([]byte(rawJSON), &msgs); err != nil {
		return fmt.Sprintf("[Recent history for channel=%s]\n%s", channelID, rawJSON)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Recent history for channel=%s]\n", channelID)
	for _, m := range msgs {
		name := m.Author
		if m.AuthorID == a.botID {
			if a.users != nil {
				if u, err := a.users.Resolve(ctx, source, m.AuthorID, m.Author); err == nil && u.DisplayName != "" {
					name = u.DisplayName
				}
			}
			name += " (self)"
		} else if a.users != nil && m.AuthorID != "" {
			if u, err := a.users.Resolve(ctx, source, m.AuthorID, m.Author); err == nil && u.DisplayName != "" {
				name = u.DisplayName
			}
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.Time, name, m.Content)
	}
	return b.String()
}
