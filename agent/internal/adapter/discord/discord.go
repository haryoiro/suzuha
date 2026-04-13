package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/voice"
)

// Compile-time checks for optional interfaces.
var _ chat.Replier = (*Chat)(nil)
var _ chat.IDSender = (*Chat)(nil)
var _ chat.Typer = (*Chat)(nil)

// Chat implements chat.Interface for Discord using discordgo.
type Chat struct {
	token           string
	botID           string
	clock           *jtime.Clock
	bus             *event.Bus
	log             *slog.Logger
	session         *discordgo.Session
	onReady         func(*discordgo.Session)
	onChannelDelete func(channelID string)
	voicePipeline   *voice.Pipeline
}

// OnChannelDelete registers a callback fired when a Discord channel is deleted.
func (c *Chat) OnChannelDelete(fn func(channelID string)) {
	c.onChannelDelete = fn
}

// OnReady registers a callback that fires after Discord connection is established.
// Use this to register Discord-dependent tools.
func (c *Chat) OnReady(fn func(*discordgo.Session)) {
	c.onReady = fn
}

// Name は gateway.Source を満たす。
func (c *Chat) Name() string { return "discord" }

// New creates a Discord chat instance.
func New(token, botID string, clock *jtime.Clock, bus *event.Bus, log *slog.Logger) *Chat {
	return &Chat{token: token, botID: botID, clock: clock, bus: bus, log: log}
}

// Run connects to Discord and starts listening for messages.
func (c *Chat) Run(ctx context.Context) error {
	session, err := discordgo.New("Bot " + c.token)
	if err != nil {
		return fmt.Errorf("discord: セッションの作成に失敗: %w", err)
	}
	c.session = session

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent | discordgo.IntentsGuildVoiceStates

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
		// ロールメンション: ボットのロールがメンションされたか確認
		if !isMention && len(m.MentionRoles) > 0 {
			if member, err := s.GuildMember(m.GuildID, s.State.User.ID); err == nil {
				for _, mentionedRole := range m.MentionRoles {
					for _, botRole := range member.Roles {
						if mentionedRole == botRole {
							isMention = true
							break
						}
					}
					if isMention {
						break
					}
				}
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

		// Extract image attachment URLs.
		var imageURLs []string
		for _, att := range m.Attachments {
			if strings.HasPrefix(att.ContentType, "image/") {
				imageURLs = append(imageURLs, att.URL)
			}
		}

		evt := c.messageToEvent(m.ChannelID, m.ID, m.Author.ID, m.Author.Username, content, isMention, isDM, m.Author.Bot, m.GuildID, guildName, channelName, imageURLs)
		c.bus.Publish(evt)
	})

	session.AddHandler(func(s *discordgo.Session, e *discordgo.ChannelDelete) {
		c.log.Info("discord チャンネルが削除された", "channel_id", e.ID, "name", e.Name)
		if c.onChannelDelete != nil {
			c.onChannelDelete(e.ID)
		}
	})

	if err := session.Open(); err != nil {
		return fmt.Errorf("discord: 接続の開始に失敗: %w", err)
	}
	defer session.Close()

	c.botID = session.State.User.ID
	c.log.Info("discord 接続しました", "user", session.State.User.Username, "id", c.botID)

	// Call onReady callback if set.
	if c.onReady != nil {
		c.onReady(session)
	}

	<-ctx.Done()
	c.log.Info("discord シャットダウン中")
	return ctx.Err()
}

// Send sends a message to a Discord channel.
// If the channel ID is actually a user ID (Unknown Channel error), it falls
// back to creating a DM channel via UserChannelCreate and retries.
func (c *Chat) Send(_ context.Context, channel string, text string) error {
	if c.session == nil {
		return fmt.Errorf("discord: セッションが初期化されていません")
	}
	resolved, err := c.resolveChannel(channel)
	if err != nil {
		return fmt.Errorf("discord: チャンネル解決に失敗: %w", err)
	}
	chunks := splitMessage(text, 2000)
	for _, chunk := range chunks {
		if _, err := c.session.ChannelMessageSend(resolved, chunk); err != nil {
			return fmt.Errorf("discord: 送信に失敗: %w", err)
		}
	}
	return nil
}

// SendWithID sends a message and returns the Discord message ID of the last chunk.
func (c *Chat) SendWithID(_ context.Context, channel string, text string) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("discord: セッションが初期化されていません")
	}
	resolved, err := c.resolveChannel(channel)
	if err != nil {
		return "", fmt.Errorf("discord: チャンネル解決に失敗: %w", err)
	}
	chunks := splitMessage(text, 2000)
	var lastID string
	for _, chunk := range chunks {
		msg, err := c.session.ChannelMessageSend(resolved, chunk)
		if err != nil {
			return "", fmt.Errorf("discord: 送信に失敗: %w", err)
		}
		lastID = msg.ID
	}
	return lastID, nil
}

// resolveChannel returns channel as-is if it's in the session state cache.
// Otherwise it tries UserChannelCreate in case channel is a user ID (for DMs).
func (c *Chat) resolveChannel(channel string) (string, error) {
	// Fast path: check in-memory state (no API call).
	if _, err := c.session.State.Channel(channel); err == nil {
		return channel, nil
	}
	// Not in state cache — could be a user ID for DM.
	ch, err := c.session.UserChannelCreate(channel)
	if err != nil {
		// Not a user ID either. Return the original channel and let the
		// caller's ChannelMessageSend surface the real Discord error.
		return channel, nil
	}
	c.log.Info("discord: ユーザーIDからDMチャンネルに解決", "user_id", channel, "dm_channel", ch.ID)
	return ch.ID, nil
}

// SendReply sends a reply to replyToID and returns the Discord message ID of the last chunk.
func (c *Chat) SendReply(_ context.Context, channel, text, replyToID string) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("discord: セッションが初期化されていません")
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
			return "", fmt.Errorf("discord: リプライの送信に失敗: %w", err)
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
func (c *Chat) messageToEvent(channel, messageID, userID, userName, content string, isMention, isDM, isBot bool, guildID, guildName, channelName string, imageURLs []string) event.Event {
	return event.NewMessageEvent(c.clock, "discord", event.MessagePayload{
		Content:     content,
		Channel:     channel,
		MessageID:   messageID,
		UserID:      userID,
		UserName:    userName,
		IsMention:   isMention,
		IsDM:        isDM,
		IsBot:       isBot,
		GuildID:     guildID,
		GuildName:   guildName,
		ChannelName: channelName,
		ImageURLs:   imageURLs,
	})
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
