package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/event"
)

// Compile-time checks for optional interfaces.
var _ chat.Replier  = (*Chat)(nil)
var _ chat.IDSender = (*Chat)(nil)
var _ chat.Typer    = (*Chat)(nil)

// Chat implements chat.Interface for Discord using discordgo.
type Chat struct {
	token   string
	botID   string
	bus     *event.Bus
	log     *slog.Logger
	session *discordgo.Session
	onReady func(*discordgo.Session)
}

// OnReady registers a callback that fires after Discord connection is established.
// Use this to register Discord-dependent tools.
func (c *Chat) OnReady(fn func(*discordgo.Session)) {
	c.onReady = fn
}

// New creates a Discord chat instance.
func New(token, botID string, bus *event.Bus, log *slog.Logger) *Chat {
	return &Chat{token: token, botID: botID, bus: bus, log: log}
}

// Run connects to Discord and starts listening for messages.
func (c *Chat) Run(ctx context.Context) error {
	session, err := discordgo.New("Bot " + c.token)
	if err != nil {
		return fmt.Errorf("discord: create session: %w", err)
	}
	c.session = session

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore own messages.
		if m.Author.ID == s.State.User.ID {
			return
		}

		isMention := false
		for _, u := range m.Mentions {
			if u.ID == s.State.User.ID {
				isMention = true
				break
			}
		}

		// Resolve mention tags to readable names.
		// Bot's own mention is stripped entirely; other users become @DisplayName.
		// Discord uses both <@ID> and <@!ID> (nickname mention) formats.
		content := m.Content
		if isMention {
			content = strings.ReplaceAll(content, "<@"+s.State.User.ID+">", "")
			content = strings.ReplaceAll(content, "<@!"+s.State.User.ID+">", "")
		}
		for _, u := range m.Mentions {
			if u.ID == s.State.User.ID {
				continue // already stripped above
			}
			displayName := u.Username
			if u.GlobalName != "" {
				displayName = u.GlobalName
			}
			content = strings.ReplaceAll(content, "<@"+u.ID+">", "@"+displayName)
			content = strings.ReplaceAll(content, "<@!"+u.ID+">", "@"+displayName)
		}
		content = strings.TrimSpace(content)

		// Detect DM (no guild = direct message).
		isDM := m.GuildID == ""

		// Resolve guild and channel names from session state cache.
		var guildName, channelName string
		if m.GuildID != "" {
			if g, err := s.State.Guild(m.GuildID); err == nil {
				guildName = g.Name
			}
		}
		if ch, err := s.State.Channel(m.ChannelID); err == nil {
			channelName = ch.Name
		}

		evt := c.messageToEvent(m.ChannelID, m.ID, m.Author.ID, m.Author.Username, content, isMention, isDM, m.GuildID, guildName, channelName)
		c.bus.Publish(evt)
	})

	if err := session.Open(); err != nil {
		return fmt.Errorf("discord: open: %w", err)
	}
	defer session.Close()

	c.botID = session.State.User.ID
	c.log.Info("discord connected", "user", session.State.User.Username, "id", c.botID)

	// Call onReady callback if set.
	if c.onReady != nil {
		c.onReady(session)
	}

	<-ctx.Done()
	c.log.Info("discord shutting down")
	return ctx.Err()
}

// Send sends a message to a Discord channel.
func (c *Chat) Send(_ context.Context, channel string, text string) error {
	if c.session == nil {
		return fmt.Errorf("discord: session not initialized")
	}
	chunks := splitMessage(text, 2000)
	for _, chunk := range chunks {
		if _, err := c.session.ChannelMessageSend(channel, chunk); err != nil {
			return fmt.Errorf("discord: send: %w", err)
		}
	}
	return nil
}

// SendWithID sends a message and returns the Discord message ID of the last chunk.
func (c *Chat) SendWithID(_ context.Context, channel string, text string) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("discord: session not initialized")
	}
	chunks := splitMessage(text, 2000)
	var lastID string
	for _, chunk := range chunks {
		msg, err := c.session.ChannelMessageSend(channel, chunk)
		if err != nil {
			return "", fmt.Errorf("discord: send: %w", err)
		}
		lastID = msg.ID
	}
	return lastID, nil
}

// SendReply sends a reply to replyToID and returns the Discord message ID of the last chunk.
func (c *Chat) SendReply(_ context.Context, channel, text, replyToID string) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("discord: session not initialized")
	}
	chunks := splitMessage(text, 2000)
	var lastID string
	for i, chunk := range chunks {
		var msg *discordgo.Message
		var err error
		if i == 0 {
			// First chunk is a reply to the target message.
			msg, err = c.session.ChannelMessageSendReply(channel, chunk, &discordgo.MessageReference{
				MessageID: replyToID,
				ChannelID: channel,
			})
		} else {
			msg, err = c.session.ChannelMessageSend(channel, chunk)
		}
		if err != nil {
			return "", fmt.Errorf("discord: send reply: %w", err)
		}
		lastID = msg.ID
	}
	return lastID, nil
}

// Typing sends a typing indicator to the specified channel.
func (c *Chat) Typing(_ context.Context, channel string) {
	if c.session == nil {
		return
	}
	_ = c.session.ChannelTyping(channel)
}

// messageToEvent converts a Discord message to an Event.
func (c *Chat) messageToEvent(channel, messageID, userID, userName, content string, isMention, isDM bool, guildID, guildName, channelName string) event.Event {
	return event.Event{
		ID:     uuid.NewString(),
		Source: "discord",
		Type:   "message",
		Payload: map[string]any{
			"content":      content,
			"channel":      channel,
			"message_id":   messageID,
			"user_id":      userID,
			"user_name":    userName,
			"is_mention":   isMention,
			"is_dm":        isDM,
			"guild_id":     guildID,
			"guild_name":   guildName,
			"channel_name": channelName,
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
