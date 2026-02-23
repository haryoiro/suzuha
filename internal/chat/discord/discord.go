package discord

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/event"
)

// Chat implements chat.Interface for Discord using discordgo.
// For now this is a skeleton — discordgo dependency will be added later.
type Chat struct {
	token string
	botID string
	bus   *event.Bus
	log   *slog.Logger
}

// New creates a Discord chat instance.
func New(token, botID string, bus *event.Bus, log *slog.Logger) *Chat {
	return &Chat{token: token, botID: botID, bus: bus, log: log}
}

// Run connects to Discord and starts listening for messages.
func (c *Chat) Run(ctx context.Context) error {
	// TODO: Initialize discordgo session, register message handler, open connection.
	// For now, just block until context is canceled.
	c.log.Info("discord chat started (stub)")
	<-ctx.Done()
	return ctx.Err()
}

// Send sends a message to a Discord channel.
func (c *Chat) Send(_ context.Context, channel string, text string) error {
	// TODO: Use discordgo session to send message.
	c.log.Info("discord send (stub)", "channel", channel, "text_len", len(text))
	return nil
}

// messageToEvent converts a Discord message to an Event.
// Used internally by the message handler.
func (c *Chat) messageToEvent(channel, userID, userName, content string, isMention bool) event.Event {
	return event.Event{
		ID:     uuid.NewString(),
		Source: "discord",
		Type:   "message",
		Payload: map[string]any{
			"content":    content,
			"channel":    channel,
			"user_id":    userID,
			"user_name":  userName,
			"is_mention": isMention,
		},
		Timestamp: time.Now(),
	}
}

// splitMessage splits a long message into chunks that fit Discord's 2000 char limit.
func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 2000
	}
	if len(text) <= maxLen {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		end := maxLen
		if end > len(text) {
			end = len(text)
		}
		// Try to split at a newline.
		if end < len(text) {
			for i := end - 1; i > end/2; i-- {
				if text[i] == '\n' {
					end = i + 1
					break
				}
			}
		}
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return chunks
}

// Ensure Chat implements chat.Interface at compile time.
var _ interface {
	Run(ctx context.Context) error
	Send(ctx context.Context, channel string, text string) error
} = (*Chat)(nil)

// suppress unused import warning
var _ = fmt.Sprintf
