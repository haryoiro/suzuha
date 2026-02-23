package builtin

import (
	"context"
	"encoding/json"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
)

// DiscordReact adds an emoji reaction to a message.
type DiscordReact struct {
	session *discordgo.Session
}

func NewDiscordReact(s *discordgo.Session) *DiscordReact {
	return &DiscordReact{session: s}
}

func (d *DiscordReact) Name() string { return "discord_react" }
func (d *DiscordReact) Description() string {
	return "Add an emoji reaction to a Discord message. Use this to react to what the user said."
}

func (d *DiscordReact) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID."},
			"message_id": {"type": "string", "description": "The message ID to react to."},
			"emoji": {"type": "string", "description": "The emoji to react with. Use Unicode emoji like 👍 or Discord custom emoji format."}
		},
		"required": ["channel_id", "message_id", "emoji"]
	}`)
}

type reactInput struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

func (d *DiscordReact) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in reactInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if err := d.session.MessageReactionAdd(in.ChannelID, in.MessageID, in.Emoji); err != nil {
		return tool.ErrorResult("react failed: " + err.Error()), nil
	}
	return tool.TextResult("reacted with " + in.Emoji), nil
}

var _ tool.Tool = (*DiscordReact)(nil)

// DiscordReply sends a reply to a specific message.
type DiscordReply struct {
	session *discordgo.Session
}

func NewDiscordReply(s *discordgo.Session) *DiscordReply {
	return &DiscordReply{session: s}
}

func (d *DiscordReply) Name() string { return "discord_reply" }
func (d *DiscordReply) Description() string {
	return "Reply to a specific Discord message. The reply will be visually linked to the original message."
}

func (d *DiscordReply) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID."},
			"message_id": {"type": "string", "description": "The message ID to reply to."},
			"content": {"type": "string", "description": "The reply text."}
		},
		"required": ["channel_id", "message_id", "content"]
	}`)
}

type replyInput struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
}

func (d *DiscordReply) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in replyInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	_, err := d.session.ChannelMessageSendReply(in.ChannelID, in.Content, &discordgo.MessageReference{
		MessageID: in.MessageID,
		ChannelID: in.ChannelID,
	})
	if err != nil {
		return tool.ErrorResult("reply failed: " + err.Error()), nil
	}
	return tool.TextResult("replied"), nil
}

var _ tool.Tool = (*DiscordReply)(nil)

// DiscordGetHistory fetches recent messages from a channel.
type DiscordGetHistory struct {
	session *discordgo.Session
}

func NewDiscordGetHistory(s *discordgo.Session) *DiscordGetHistory {
	return &DiscordGetHistory{session: s}
}

func (d *DiscordGetHistory) Name() string { return "discord_get_history" }
func (d *DiscordGetHistory) Description() string {
	return "Get recent messages from a Discord channel to understand the conversation context."
}

func (d *DiscordGetHistory) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID."},
			"limit": {"type": "integer", "description": "Number of messages to fetch (max 50).", "default": 10}
		},
		"required": ["channel_id"]
	}`)
}

type historyInput struct {
	ChannelID string `json:"channel_id"`
	Limit     int    `json:"limit"`
}

func (d *DiscordGetHistory) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in historyInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 10
	}
	msgs, err := d.session.ChannelMessages(in.ChannelID, in.Limit, "", "", "")
	if err != nil {
		return tool.ErrorResult("get history failed: " + err.Error()), nil
	}
	type msgOut struct {
		ID       string `json:"id"`
		AuthorID string `json:"author_id"`
		Author   string `json:"author"`
		Content  string `json:"content"`
		Time     string `json:"time"`
	}
	// Discord returns newest-first; reverse to chronological order.
	out := make([]msgOut, len(msgs))
	for i, m := range msgs {
		out[len(msgs)-1-i] = msgOut{
			ID:       m.ID,
			AuthorID: m.Author.ID,
			Author:   m.Author.Username,
			Content:  m.Content,
			Time:     m.Timestamp.Format("15:04:05"),
		}
	}
	b, _ := json.Marshal(out)
	return tool.TextResult(string(b)), nil
}

var _ tool.Tool = (*DiscordGetHistory)(nil)

// DiscordSendDM sends a direct message to a Discord user.
type DiscordSendDM struct {
	session *discordgo.Session
}

func NewDiscordSendDM(s *discordgo.Session) *DiscordSendDM {
	return &DiscordSendDM{session: s}
}

func (d *DiscordSendDM) Name() string { return "discord_send_dm" }
func (d *DiscordSendDM) Description() string {
	return "Send a direct message to a Discord user by their user ID."
}

func (d *DiscordSendDM) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id": {"type": "string", "description": "The Discord user ID to send a DM to."},
			"content": {"type": "string", "description": "The message text."}
		},
		"required": ["user_id", "content"]
	}`)
}

type sendDMInput struct {
	UserID  string `json:"user_id"`
	Content string `json:"content"`
}

func (d *DiscordSendDM) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in sendDMInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("invalid input: " + err.Error()), nil
	}
	ch, err := d.session.UserChannelCreate(in.UserID)
	if err != nil {
		return tool.ErrorResult("create DM channel failed: " + err.Error()), nil
	}
	_, err = d.session.ChannelMessageSend(ch.ID, in.Content)
	if err != nil {
		return tool.ErrorResult("send DM failed: " + err.Error()), nil
	}
	return tool.TextResult("DM sent"), nil
}

var _ tool.Tool = (*DiscordSendDM)(nil)
