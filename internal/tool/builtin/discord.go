package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if err := d.session.MessageReactionAdd(in.ChannelID, in.MessageID, in.Emoji); err != nil {
		return tool.ErrorResult("リアクション失敗: " + err.Error()), nil
	}
	return tool.StopResult("リアクション済み: " + in.Emoji), nil
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
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	_, err := d.session.ChannelMessageSendReply(in.ChannelID, in.Content, &discordgo.MessageReference{
		MessageID: in.MessageID,
		ChannelID: in.ChannelID,
	})
	if err != nil {
		return tool.ErrorResult("返信失敗: " + err.Error()), nil
	}
	return tool.TextResult("返信しました"), nil
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
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 10
	}
	msgs, err := d.session.ChannelMessages(in.ChannelID, in.Limit, "", "", "")
	if err != nil {
		return tool.ErrorResult("履歴取得失敗: " + err.Error()), nil
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
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	ch, err := d.session.UserChannelCreate(in.UserID)
	if err != nil {
		return tool.ErrorResult("DMチャンネル作成失敗: " + err.Error()), nil
	}
	_, err = d.session.ChannelMessageSend(ch.ID, in.Content)
	if err != nil {
		return tool.ErrorResult("DM送信失敗: " + err.Error()), nil
	}
	return tool.TextResult("DMを送信しました"), nil
}

var _ tool.Tool = (*DiscordSendDM)(nil)

// ───────────────────────────────────────────────
// Channel management
// ───────────────────────────────────────────────

// DiscordCreateChannel creates a new text channel in a guild.
type DiscordCreateChannel struct{ session *discordgo.Session }

func NewDiscordCreateChannel(s *discordgo.Session) *DiscordCreateChannel {
	return &DiscordCreateChannel{session: s}
}
func (d *DiscordCreateChannel) Name() string { return "discord_create_channel" }
func (d *DiscordCreateChannel) Description() string {
	return "Create a new text channel in the server."
}
func (d *DiscordCreateChannel) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id":    {"type": "string", "description": "The server (guild) ID."},
			"name":        {"type": "string", "description": "Channel name (lowercase, no spaces — use hyphens)."},
			"topic":       {"type": "string", "description": "Channel topic/description (optional)."},
			"category_id": {"type": "string", "description": "Parent category ID (optional)."}
		},
		"required": ["guild_id", "name"]
	}`)
}

func (d *DiscordCreateChannel) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID    string `json:"guild_id"`
		Name       string `json:"name"`
		Topic      string `json:"topic"`
		CategoryID string `json:"category_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	data := discordgo.GuildChannelCreateData{
		Name:  in.Name,
		Type:  discordgo.ChannelTypeGuildText,
		Topic: in.Topic,
	}
	if in.CategoryID != "" {
		data.ParentID = in.CategoryID
	}
	ch, err := d.session.GuildChannelCreateComplex(in.GuildID, data)
	if err != nil {
		return tool.ErrorResult("チャンネル作成失敗: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]string{"id": ch.ID, "name": ch.Name})
	return tool.TextResult(string(out)), nil
}

var _ tool.Tool = (*DiscordCreateChannel)(nil)

// DiscordEditChannel edits a channel's name or topic.
type DiscordEditChannel struct{ session *discordgo.Session }

func NewDiscordEditChannel(s *discordgo.Session) *DiscordEditChannel {
	return &DiscordEditChannel{session: s}
}
func (d *DiscordEditChannel) Name() string { return "discord_edit_channel" }
func (d *DiscordEditChannel) Description() string {
	return "Edit a channel's name or topic."
}
func (d *DiscordEditChannel) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID to edit."},
			"name":       {"type": "string", "description": "New channel name (optional)."},
			"topic":      {"type": "string", "description": "New channel topic (optional)."}
		},
		"required": ["channel_id"]
	}`)
}

func (d *DiscordEditChannel) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		ChannelID string  `json:"channel_id"`
		Name      *string `json:"name"`
		Topic     *string `json:"topic"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	edit := &discordgo.ChannelEdit{}
	if in.Name != nil {
		edit.Name = *in.Name
	}
	if in.Topic != nil {
		edit.Topic = *in.Topic
	}
	_, err := d.session.ChannelEdit(in.ChannelID, edit)
	if err != nil {
		return tool.ErrorResult("チャンネル編集失敗: " + err.Error()), nil
	}
	return tool.TextResult("チャンネルを更新しました"), nil
}

var _ tool.Tool = (*DiscordEditChannel)(nil)

// DiscordDeleteChannel deletes a channel.
type DiscordDeleteChannel struct{ session *discordgo.Session }

func NewDiscordDeleteChannel(s *discordgo.Session) *DiscordDeleteChannel {
	return &DiscordDeleteChannel{session: s}
}
func (d *DiscordDeleteChannel) Name() string { return "discord_delete_channel" }
func (d *DiscordDeleteChannel) Description() string {
	return "Delete a channel. This is irreversible — use with caution."
}
func (d *DiscordDeleteChannel) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID to delete."}
		},
		"required": ["channel_id"]
	}`)
}

func (d *DiscordDeleteChannel) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	_, err := d.session.ChannelDelete(in.ChannelID)
	if err != nil {
		return tool.ErrorResult("チャンネル削除失敗: " + err.Error()), nil
	}
	return tool.TextResult("チャンネルを削除しました"), nil
}

var _ tool.Tool = (*DiscordDeleteChannel)(nil)

// DiscordListChannels lists all channels in a guild.
type DiscordListChannels struct{ session *discordgo.Session }

func NewDiscordListChannels(s *discordgo.Session) *DiscordListChannels {
	return &DiscordListChannels{session: s}
}
func (d *DiscordListChannels) Name() string { return "discord_list_channels" }
func (d *DiscordListChannels) Description() string {
	return "List all channels in the server."
}
func (d *DiscordListChannels) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."}
		},
		"required": ["guild_id"]
	}`)
}

func (d *DiscordListChannels) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	channels, err := d.session.GuildChannels(in.GuildID)
	if err != nil {
		return tool.ErrorResult("チャンネル一覧取得失敗: " + err.Error()), nil
	}
	type chOut struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Topic    string `json:"topic,omitempty"`
		ParentID string `json:"parent_id,omitempty"`
	}
	out := make([]chOut, 0, len(channels))
	for _, ch := range channels {
		typeName := "text"
		switch ch.Type {
		case discordgo.ChannelTypeGuildVoice:
			typeName = "voice"
		case discordgo.ChannelTypeGuildCategory:
			typeName = "category"
		case discordgo.ChannelTypeGuildForum:
			typeName = "forum"
		case discordgo.ChannelTypeGuildStageVoice:
			typeName = "stage"
		}
		out = append(out, chOut{
			ID: ch.ID, Name: ch.Name, Type: typeName,
			Topic: ch.Topic, ParentID: ch.ParentID,
		})
	}
	b, _ := json.Marshal(out)
	return tool.TextResult(string(b)), nil
}

var _ tool.Tool = (*DiscordListChannels)(nil)

// ───────────────────────────────────────────────
// Member management
// ───────────────────────────────────────────────

// DiscordKickMember kicks a member from the guild.
type DiscordKickMember struct{ session *discordgo.Session }

func NewDiscordKickMember(s *discordgo.Session) *DiscordKickMember {
	return &DiscordKickMember{session: s}
}
func (d *DiscordKickMember) Name() string { return "discord_kick_member" }
func (d *DiscordKickMember) Description() string {
	return "Kick a member from the server. Requires Kick Members permission."
}
func (d *DiscordKickMember) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"user_id":  {"type": "string", "description": "The user ID to kick."},
			"reason":   {"type": "string", "description": "Reason for the kick (optional)."}
		},
		"required": ["guild_id", "user_id"]
	}`)
}

func (d *DiscordKickMember) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
		UserID  string `json:"user_id"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	err := d.session.GuildMemberDeleteWithReason(in.GuildID, in.UserID, in.Reason)
	if err != nil {
		return tool.ErrorResult("キック失敗: " + err.Error()), nil
	}
	return tool.TextResult("メンバーをキックしました"), nil
}

var _ tool.Tool = (*DiscordKickMember)(nil)

// DiscordBanMember bans a member from the guild.
type DiscordBanMember struct{ session *discordgo.Session }

func NewDiscordBanMember(s *discordgo.Session) *DiscordBanMember {
	return &DiscordBanMember{session: s}
}
func (d *DiscordBanMember) Name() string { return "discord_ban_member" }
func (d *DiscordBanMember) Description() string {
	return "Ban a member from the server. Requires Ban Members permission."
}
func (d *DiscordBanMember) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"user_id":  {"type": "string", "description": "The user ID to ban."},
			"reason":   {"type": "string", "description": "Reason for the ban (optional)."},
			"delete_days": {"type": "integer", "description": "Number of days of messages to delete (0-7, default 0)."}
		},
		"required": ["guild_id", "user_id"]
	}`)
}

func (d *DiscordBanMember) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID    string `json:"guild_id"`
		UserID     string `json:"user_id"`
		Reason     string `json:"reason"`
		DeleteDays int    `json:"delete_days"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	err := d.session.GuildBanCreateWithReason(in.GuildID, in.UserID, in.Reason, in.DeleteDays)
	if err != nil {
		return tool.ErrorResult("BAN失敗: " + err.Error()), nil
	}
	return tool.TextResult("メンバーをBANしました"), nil
}

var _ tool.Tool = (*DiscordBanMember)(nil)

// DiscordTimeoutMember applies a communication timeout to a member.
type DiscordTimeoutMember struct{ session *discordgo.Session }

func NewDiscordTimeoutMember(s *discordgo.Session) *DiscordTimeoutMember {
	return &DiscordTimeoutMember{session: s}
}
func (d *DiscordTimeoutMember) Name() string { return "discord_timeout_member" }
func (d *DiscordTimeoutMember) Description() string {
	return "Timeout (mute) a member for a specified duration. Set minutes to 0 to remove timeout."
}
func (d *DiscordTimeoutMember) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"user_id":  {"type": "string", "description": "The user ID to timeout."},
			"minutes":  {"type": "integer", "description": "Duration in minutes (0 to remove timeout, max 40320 = 28 days)."}
		},
		"required": ["guild_id", "user_id", "minutes"]
	}`)
}

func (d *DiscordTimeoutMember) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
		UserID  string `json:"user_id"`
		Minutes int    `json:"minutes"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	var until *time.Time
	if in.Minutes > 0 {
		t := time.Now().Add(time.Duration(in.Minutes) * time.Minute)
		until = &t
	}
	_, err := d.session.GuildMemberEdit(in.GuildID, in.UserID, &discordgo.GuildMemberParams{
		CommunicationDisabledUntil: until,
	})
	if err != nil {
		return tool.ErrorResult("タイムアウト失敗: " + err.Error()), nil
	}
	if in.Minutes == 0 {
		return tool.TextResult("タイムアウトを解除しました"), nil
	}
	return tool.TextResult(fmt.Sprintf("メンバーを%d分間タイムアウトしました", in.Minutes)), nil
}

var _ tool.Tool = (*DiscordTimeoutMember)(nil)

// DiscordListMembers lists members in a guild.
type DiscordListMembers struct{ session *discordgo.Session }

func NewDiscordListMembers(s *discordgo.Session) *DiscordListMembers {
	return &DiscordListMembers{session: s}
}
func (d *DiscordListMembers) Name() string { return "discord_list_members" }
func (d *DiscordListMembers) Description() string {
	return "List members in the server (up to 100)."
}
func (d *DiscordListMembers) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"limit":    {"type": "integer", "description": "Max members to return (1-100, default 50)."}
		},
		"required": ["guild_id"]
	}`)
}

func (d *DiscordListMembers) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 50
	}
	members, err := d.session.GuildMembers(in.GuildID, "", in.Limit)
	if err != nil {
		return tool.ErrorResult("メンバー一覧取得失敗: " + err.Error()), nil
	}
	type mOut struct {
		UserID   string   `json:"user_id"`
		Username string   `json:"username"`
		Nick     string   `json:"nick,omitempty"`
		Roles    []string `json:"roles"`
		Bot      bool     `json:"bot"`
	}
	out := make([]mOut, 0, len(members))
	for _, m := range members {
		out = append(out, mOut{
			UserID:   m.User.ID,
			Username: m.User.Username,
			Nick:     m.Nick,
			Roles:    m.Roles,
			Bot:      m.User.Bot,
		})
	}
	b, _ := json.Marshal(out)
	return tool.TextResult(string(b)), nil
}

var _ tool.Tool = (*DiscordListMembers)(nil)

// ───────────────────────────────────────────────
// Message management
// ───────────────────────────────────────────────

// DiscordDeleteMessage deletes a message.
type DiscordDeleteMessage struct{ session *discordgo.Session }

func NewDiscordDeleteMessage(s *discordgo.Session) *DiscordDeleteMessage {
	return &DiscordDeleteMessage{session: s}
}
func (d *DiscordDeleteMessage) Name() string { return "discord_delete_message" }
func (d *DiscordDeleteMessage) Description() string {
	return "Delete a message from a channel."
}
func (d *DiscordDeleteMessage) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID."},
			"message_id": {"type": "string", "description": "The message ID to delete."}
		},
		"required": ["channel_id", "message_id"]
	}`)
}

func (d *DiscordDeleteMessage) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if err := d.session.ChannelMessageDelete(in.ChannelID, in.MessageID); err != nil {
		return tool.ErrorResult("メッセージ削除失敗: " + err.Error()), nil
	}
	return tool.TextResult("メッセージを削除しました"), nil
}

var _ tool.Tool = (*DiscordDeleteMessage)(nil)

// DiscordPinMessage pins or unpins a message.
type DiscordPinMessage struct{ session *discordgo.Session }

func NewDiscordPinMessage(s *discordgo.Session) *DiscordPinMessage {
	return &DiscordPinMessage{session: s}
}
func (d *DiscordPinMessage) Name() string { return "discord_pin_message" }
func (d *DiscordPinMessage) Description() string {
	return "Pin or unpin a message in a channel."
}
func (d *DiscordPinMessage) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The channel ID."},
			"message_id": {"type": "string", "description": "The message ID."},
			"pin":        {"type": "boolean", "description": "true to pin, false to unpin."}
		},
		"required": ["channel_id", "message_id", "pin"]
	}`)
}

func (d *DiscordPinMessage) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		ChannelID string `json:"channel_id"`
		MessageID string `json:"message_id"`
		Pin       bool   `json:"pin"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	var err error
	if in.Pin {
		err = d.session.ChannelMessagePin(in.ChannelID, in.MessageID)
	} else {
		err = d.session.ChannelMessageUnpin(in.ChannelID, in.MessageID)
	}
	if err != nil {
		return tool.ErrorResult("ピン留め/解除失敗: " + err.Error()), nil
	}
	if in.Pin {
		return tool.TextResult("メッセージをピン留めしました"), nil
	}
	return tool.TextResult("メッセージのピン留めを解除しました"), nil
}

var _ tool.Tool = (*DiscordPinMessage)(nil)

// ───────────────────────────────────────────────
// Role management
// ───────────────────────────────────────────────

// DiscordAddRole adds a role to a member.
type DiscordAddRole struct{ session *discordgo.Session }

func NewDiscordAddRole(s *discordgo.Session) *DiscordAddRole {
	return &DiscordAddRole{session: s}
}
func (d *DiscordAddRole) Name() string { return "discord_add_role" }
func (d *DiscordAddRole) Description() string {
	return "Add a role to a server member."
}
func (d *DiscordAddRole) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"user_id":  {"type": "string", "description": "The user ID."},
			"role_id":  {"type": "string", "description": "The role ID to add."}
		},
		"required": ["guild_id", "user_id", "role_id"]
	}`)
}

func (d *DiscordAddRole) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
		UserID  string `json:"user_id"`
		RoleID  string `json:"role_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if err := d.session.GuildMemberRoleAdd(in.GuildID, in.UserID, in.RoleID); err != nil {
		return tool.ErrorResult("ロール付与失敗: " + err.Error()), nil
	}
	return tool.TextResult("ロールを付与しました"), nil
}

var _ tool.Tool = (*DiscordAddRole)(nil)

// DiscordRemoveRole removes a role from a member.
type DiscordRemoveRole struct{ session *discordgo.Session }

func NewDiscordRemoveRole(s *discordgo.Session) *DiscordRemoveRole {
	return &DiscordRemoveRole{session: s}
}
func (d *DiscordRemoveRole) Name() string { return "discord_remove_role" }
func (d *DiscordRemoveRole) Description() string {
	return "Remove a role from a server member."
}
func (d *DiscordRemoveRole) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."},
			"user_id":  {"type": "string", "description": "The user ID."},
			"role_id":  {"type": "string", "description": "The role ID to remove."}
		},
		"required": ["guild_id", "user_id", "role_id"]
	}`)
}

func (d *DiscordRemoveRole) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
		UserID  string `json:"user_id"`
		RoleID  string `json:"role_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if err := d.session.GuildMemberRoleRemove(in.GuildID, in.UserID, in.RoleID); err != nil {
		return tool.ErrorResult("ロール削除失敗: " + err.Error()), nil
	}
	return tool.TextResult("ロールを削除しました"), nil
}

var _ tool.Tool = (*DiscordRemoveRole)(nil)

// DiscordListRoles lists all roles in a guild.
type DiscordListRoles struct{ session *discordgo.Session }

func NewDiscordListRoles(s *discordgo.Session) *DiscordListRoles {
	return &DiscordListRoles{session: s}
}
func (d *DiscordListRoles) Name() string { return "discord_list_roles" }
func (d *DiscordListRoles) Description() string {
	return "List all roles in the server."
}
func (d *DiscordListRoles) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."}
		},
		"required": ["guild_id"]
	}`)
}

func (d *DiscordListRoles) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	roles, err := d.session.GuildRoles(in.GuildID)
	if err != nil {
		return tool.ErrorResult("ロール一覧取得失敗: " + err.Error()), nil
	}
	type rOut struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color int    `json:"color"`
	}
	out := make([]rOut, 0, len(roles))
	for _, r := range roles {
		out = append(out, rOut{ID: r.ID, Name: r.Name, Color: r.Color})
	}
	b, _ := json.Marshal(out)
	return tool.TextResult(string(b)), nil
}

var _ tool.Tool = (*DiscordListRoles)(nil)

// ───────────────────────────────────────────────
// Server info
// ───────────────────────────────────────────────

// DiscordServerInfo retrieves basic guild information.
type DiscordServerInfo struct{ session *discordgo.Session }

func NewDiscordServerInfo(s *discordgo.Session) *DiscordServerInfo {
	return &DiscordServerInfo{session: s}
}
func (d *DiscordServerInfo) Name() string { return "discord_server_info" }
func (d *DiscordServerInfo) Description() string {
	return "Get basic information about the server (name, member count, owner, etc.)."
}
func (d *DiscordServerInfo) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "The server (guild) ID."}
		},
		"required": ["guild_id"]
	}`)
}

func (d *DiscordServerInfo) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		GuildID string `json:"guild_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	g, err := d.session.Guild(in.GuildID)
	if err != nil {
		return tool.ErrorResult("サーバー情報取得失敗: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{
		"id":           g.ID,
		"name":         g.Name,
		"owner_id":     g.OwnerID,
		"member_count": g.MemberCount,
		"description":  g.Description,
	})
	return tool.TextResult(string(out)), nil
}

var _ tool.Tool = (*DiscordServerInfo)(nil)

// ───────────────────────────────────────────────
// Thread management
// ───────────────────────────────────────────────

// DiscordCreateThread creates a thread from a message or as a standalone thread.
type DiscordCreateThread struct{ session *discordgo.Session }

func NewDiscordCreateThread(s *discordgo.Session) *DiscordCreateThread {
	return &DiscordCreateThread{session: s}
}
func (d *DiscordCreateThread) Name() string { return "discord_create_thread" }
func (d *DiscordCreateThread) Description() string {
	return "Create a thread in a channel, optionally attached to an existing message."
}
func (d *DiscordCreateThread) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"channel_id": {"type": "string", "description": "The parent channel ID."},
			"name":       {"type": "string", "description": "Thread name."},
			"message_id": {"type": "string", "description": "Message ID to start the thread from (optional). Omit for standalone thread."}
		},
		"required": ["channel_id", "name"]
	}`)
}

func (d *DiscordCreateThread) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	var ch *discordgo.Channel
	var err error
	if in.MessageID != "" {
		ch, err = d.session.MessageThreadStartComplex(in.ChannelID, in.MessageID, &discordgo.ThreadStart{
			Name: in.Name,
			Type: discordgo.ChannelTypeGuildPublicThread,
		})
	} else {
		ch, err = d.session.ThreadStartComplex(in.ChannelID, &discordgo.ThreadStart{
			Name: in.Name,
			Type: discordgo.ChannelTypeGuildPublicThread,
		})
	}
	if err != nil {
		return tool.ErrorResult("スレッド作成失敗: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]string{"id": ch.ID, "name": ch.Name})
	return tool.TextResult(string(out)), nil
}

var _ tool.Tool = (*DiscordCreateThread)(nil)

// ───────────────────────────────────────────────
// Bot presence / status
// ───────────────────────────────────────────────

// DiscordUpdateStatus changes the bot's online presence and activity.
type DiscordUpdateStatus struct{ session *discordgo.Session }

func NewDiscordUpdateStatus(s *discordgo.Session) *DiscordUpdateStatus {
	return &DiscordUpdateStatus{session: s}
}
func (d *DiscordUpdateStatus) Name() string { return "discord_update_status" }
func (d *DiscordUpdateStatus) Description() string {
	return "Botの表示状態とアクティビティを変更する。気分や行動に合わせて自由に設定できる。"
}
func (d *DiscordUpdateStatus) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"status": {
				"type": "string",
				"enum": ["online", "idle", "dnd", "invisible"],
				"description": "オンライン状態。online=オンライン, idle=退席中, dnd=取り込み中, invisible=オフライン表示"
			},
			"activity_type": {
				"type": "string",
				"enum": ["playing", "listening", "watching", "competing", "custom"],
				"description": "アクティビティの種類"
			},
			"activity_text": {
				"type": "string",
				"description": "アクティビティのテキスト（例: 'ネットサーフィン', 'みんなの会話', 'プログラミング'）"
			}
		}
	}`)
}

func (d *DiscordUpdateStatus) Execute(_ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Status       string `json:"status"`
		ActivityType string `json:"activity_type"`
		ActivityText string `json:"activity_text"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}

	status := in.Status
	if status == "" {
		status = "online"
	}

	data := discordgo.UpdateStatusData{Status: status}

	if in.ActivityText != "" {
		actType := discordgo.ActivityTypeGame // default: "Playing"
		switch in.ActivityType {
		case "listening":
			actType = discordgo.ActivityTypeListening
		case "watching":
			actType = discordgo.ActivityTypeWatching
		case "competing":
			actType = discordgo.ActivityTypeCompeting
		case "custom":
			actType = discordgo.ActivityTypeCustom
		}

		activity := &discordgo.Activity{
			Name: in.ActivityText,
			Type: actType,
		}
		if actType == discordgo.ActivityTypeCustom {
			activity.State = in.ActivityText
		}
		data.Activities = []*discordgo.Activity{activity}
	}

	if err := d.session.UpdateStatusComplex(data); err != nil {
		return tool.ErrorResult("ステータス更新失敗: " + err.Error()), nil
	}

	desc := "status=" + status
	if in.ActivityText != "" {
		at := in.ActivityType
		if at == "" {
			at = "playing"
		}
		desc += ", " + at + "=" + in.ActivityText
	}
	return tool.TextResult("更新しました: " + desc), nil
}

var _ tool.Tool = (*DiscordUpdateStatus)(nil)
