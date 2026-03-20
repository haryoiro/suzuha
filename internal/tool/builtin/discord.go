package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
)

// discordTool is a generic tool backed by a discordgo.Session.
type discordTool struct {
	session *discordgo.Session
	name    string
	desc    string
	schema  json.RawMessage
	fn      func(*discordgo.Session, context.Context, json.RawMessage) (*tool.ToolResult, error)
}

func (d *discordTool) Name() string                { return d.name }
func (d *discordTool) Description() string          { return d.desc }
func (d *discordTool) InputSchema() json.RawMessage { return d.schema }
func (d *discordTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return d.fn(d.session, ctx, input)
}

var _ tool.Tool = (*discordTool)(nil)

// unmarshal is a helper that unmarshals input and returns an error result on failure.
func unmarshal[T any](input json.RawMessage) (T, *tool.ToolResult) {
	var v T
	if err := json.Unmarshal(input, &v); err != nil {
		return v, tool.ErrorResult("無効な入力: " + err.Error())
	}
	return v, nil
}

// NewDiscordTools returns all Discord tools for the given session.
func NewDiscordTools(s *discordgo.Session) []tool.Tool {
	return []tool.Tool{
		newDiscordReact(s),
		newDiscordReply(s),
		newDiscordGetHistory(s),
		newDiscordSendDM(s),
		newDiscordCreateChannel(s),
		newDiscordEditChannel(s),
		newDiscordDeleteChannel(s),
		newDiscordListChannels(s),
		newDiscordKickMember(s),
		newDiscordBanMember(s),
		newDiscordTimeoutMember(s),
		newDiscordListMembers(s),
		newDiscordDeleteMessage(s),
		newDiscordPinMessage(s),
		newDiscordAddRole(s),
		newDiscordRemoveRole(s),
		newDiscordListRoles(s),
		newDiscordServerInfo(s),
		newDiscordCreateThread(s),
		newDiscordRenameServer(s),
		newDiscordSetNickname(s),
		newDiscordUpdateStatus(s),
	}
}

// ───────────────────────────────────────────────
// Message actions
// ───────────────────────────────────────────────

func newDiscordReact(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_react",
		desc:    "メッセージにリアクションをつける。気持ちを絵文字で伝えたいときに。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID."},
				"message_id": {"type": "string", "description": "The message ID to react to."},
				"emoji": {"type": "string", "description": "The emoji to react with. Use Unicode emoji like 👍 or Discord custom emoji format."}
			},
			"required": ["channel_id", "message_id", "emoji"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				MessageID string `json:"message_id"`
				Emoji     string `json:"emoji"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.MessageReactionAdd(in.ChannelID, in.MessageID, in.Emoji); err != nil {
				return tool.ErrorResult("リアクション失敗: " + err.Error()), nil
			}
			return tool.StopResult("リアクション済み: " + in.Emoji), nil
		},
	}
}

func newDiscordReply(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_reply",
		desc:    "特定のメッセージに返信する。元のメッセージと紐付けて表示される。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID."},
				"message_id": {"type": "string", "description": "The message ID to reply to."},
				"content": {"type": "string", "description": "The reply text."}
			},
			"required": ["channel_id", "message_id", "content"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				MessageID string `json:"message_id"`
				Content   string `json:"content"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			_, err := s.ChannelMessageSendReply(in.ChannelID, in.Content, &discordgo.MessageReference{
				MessageID: in.MessageID,
				ChannelID: in.ChannelID,
			})
			if err != nil {
				return tool.ErrorResult("返信失敗: " + err.Error()), nil
			}
			return tool.TextResult("返信しました"), nil
		},
	}
}

func newDiscordGetHistory(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_get_history",
		desc:    "チャンネルの最近の会話を見て、流れを把握する。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID."},
				"limit": {"type": "integer", "description": "Number of messages to fetch (max 50).", "default": 10}
			},
			"required": ["channel_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				Limit     int    `json:"limit"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if in.Limit <= 0 || in.Limit > 50 {
				in.Limit = 10
			}
			msgs, err := s.ChannelMessages(in.ChannelID, in.Limit, "", "", "")
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
		},
	}
}

func newDiscordSendDM(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_send_dm",
		desc:    "ユーザーにDMを送る。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"user_id": {"type": "string", "description": "The Discord user ID to send a DM to."},
				"content": {"type": "string", "description": "The message text."}
			},
			"required": ["user_id", "content"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				UserID  string `json:"user_id"`
				Content string `json:"content"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			ch, err := s.UserChannelCreate(in.UserID)
			if err != nil {
				return tool.ErrorResult("DMチャンネル作成失敗: " + err.Error()), nil
			}
			_, err = s.ChannelMessageSend(ch.ID, in.Content)
			if err != nil {
				return tool.ErrorResult("DM送信失敗: " + err.Error()), nil
			}
			return tool.TextResult("DMを送信しました"), nil
		},
	}
}

func newDiscordDeleteMessage(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_delete_message",
		desc:    "メッセージを削除する。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID."},
				"message_id": {"type": "string", "description": "The message ID to delete."}
			},
			"required": ["channel_id", "message_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				MessageID string `json:"message_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.ChannelMessageDelete(in.ChannelID, in.MessageID); err != nil {
				return tool.ErrorResult("メッセージ削除失敗: " + err.Error()), nil
			}
			return tool.TextResult("メッセージを削除しました"), nil
		},
	}
}

func newDiscordPinMessage(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_pin_message",
		desc:    "メッセージをピン留め、またはピン留め解除する。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID."},
				"message_id": {"type": "string", "description": "The message ID."},
				"pin":        {"type": "boolean", "description": "true to pin, false to unpin."}
			},
			"required": ["channel_id", "message_id", "pin"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				MessageID string `json:"message_id"`
				Pin       bool   `json:"pin"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			var err error
			if in.Pin {
				err = s.ChannelMessagePin(in.ChannelID, in.MessageID)
			} else {
				err = s.ChannelMessageUnpin(in.ChannelID, in.MessageID)
			}
			if err != nil {
				return tool.ErrorResult("ピン留め/解除失敗: " + err.Error()), nil
			}
			if in.Pin {
				return tool.TextResult("メッセージをピン留めしました"), nil
			}
			return tool.TextResult("メッセージのピン留めを解除しました"), nil
		},
	}
}

// ───────────────────────────────────────────────
// Channel management
// ───────────────────────────────────────────────

func newDiscordCreateChannel(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_create_channel",
		desc:    "サーバーに新しいテキストチャンネルを作る。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id":    {"type": "string", "description": "The server (guild) ID."},
				"name":        {"type": "string", "description": "Channel name (lowercase, no spaces — use hyphens)."},
				"topic":       {"type": "string", "description": "Channel topic/description (optional)."},
				"category_id": {"type": "string", "description": "Parent category ID (optional)."}
			},
			"required": ["guild_id", "name"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID    string `json:"guild_id"`
				Name       string `json:"name"`
				Topic      string `json:"topic"`
				CategoryID string `json:"category_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			data := discordgo.GuildChannelCreateData{
				Name:  in.Name,
				Type:  discordgo.ChannelTypeGuildText,
				Topic: in.Topic,
			}
			if in.CategoryID != "" {
				data.ParentID = in.CategoryID
			}
			ch, err := s.GuildChannelCreateComplex(in.GuildID, data)
			if err != nil {
				return tool.ErrorResult("チャンネル作成失敗: " + err.Error()), nil
			}
			out, _ := json.Marshal(map[string]string{"id": ch.ID, "name": ch.Name})
			return tool.TextResult(string(out)), nil
		},
	}
}

func newDiscordEditChannel(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_edit_channel",
		desc:    "チャンネルの名前やトピックを変える。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID to edit."},
				"name":       {"type": "string", "description": "New channel name (optional)."},
				"topic":      {"type": "string", "description": "New channel topic (optional)."}
			},
			"required": ["channel_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string  `json:"channel_id"`
				Name      *string `json:"name"`
				Topic     *string `json:"topic"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			edit := &discordgo.ChannelEdit{}
			if in.Name != nil {
				edit.Name = *in.Name
			}
			if in.Topic != nil {
				edit.Topic = *in.Topic
			}
			_, err := s.ChannelEdit(in.ChannelID, edit)
			if err != nil {
				return tool.ErrorResult("チャンネル編集失敗: " + err.Error()), nil
			}
			return tool.TextResult("チャンネルを更新しました"), nil
		},
	}
}

func newDiscordDeleteChannel(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_delete_channel",
		desc:    "チャンネルを削除する。元に戻せないので慎重に。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The channel ID to delete."}
			},
			"required": ["channel_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			_, err := s.ChannelDelete(in.ChannelID)
			if err != nil {
				return tool.ErrorResult("チャンネル削除失敗: " + err.Error()), nil
			}
			return tool.TextResult("チャンネルを削除しました"), nil
		},
	}
}

func newDiscordListChannels(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_list_channels",
		desc:    "サーバーのチャンネル一覧を見る。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."}
			},
			"required": ["guild_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			channels, err := s.GuildChannels(in.GuildID)
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
		},
	}
}

// ───────────────────────────────────────────────
// Member management
// ───────────────────────────────────────────────

func newDiscordKickMember(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_kick_member",
		desc:    "メンバーをサーバーからキックする。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID to kick."},
				"reason":   {"type": "string", "description": "Reason for the kick (optional)."}
			},
			"required": ["guild_id", "user_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				UserID  string `json:"user_id"`
				Reason  string `json:"reason"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.GuildMemberDeleteWithReason(in.GuildID, in.UserID, in.Reason); err != nil {
				return tool.ErrorResult("キック失敗: " + err.Error()), nil
			}
			return tool.TextResult("メンバーをキックしました"), nil
		},
	}
}

func newDiscordBanMember(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_ban_member",
		desc:    "メンバーをサーバーからBANする。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID to ban."},
				"reason":   {"type": "string", "description": "Reason for the ban (optional)."},
				"delete_days": {"type": "integer", "description": "Number of days of messages to delete (0-7, default 0)."}
			},
			"required": ["guild_id", "user_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID    string `json:"guild_id"`
				UserID     string `json:"user_id"`
				Reason     string `json:"reason"`
				DeleteDays int    `json:"delete_days"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.GuildBanCreateWithReason(in.GuildID, in.UserID, in.Reason, in.DeleteDays); err != nil {
				return tool.ErrorResult("BAN失敗: " + err.Error()), nil
			}
			return tool.TextResult("メンバーをBANしました"), nil
		},
	}
}

func newDiscordTimeoutMember(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_timeout_member",
		desc:    "メンバーをタイムアウト（ミュート）する。0分で解除。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID to timeout."},
				"minutes":  {"type": "integer", "description": "Duration in minutes (0 to remove timeout, max 40320 = 28 days)."}
			},
			"required": ["guild_id", "user_id", "minutes"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				UserID  string `json:"user_id"`
				Minutes int    `json:"minutes"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			var until *time.Time
			if in.Minutes > 0 {
				t := time.Now().Add(time.Duration(in.Minutes) * time.Minute)
				until = &t
			}
			_, err := s.GuildMemberEdit(in.GuildID, in.UserID, &discordgo.GuildMemberParams{
				CommunicationDisabledUntil: until,
			})
			if err != nil {
				return tool.ErrorResult("タイムアウト失敗: " + err.Error()), nil
			}
			if in.Minutes == 0 {
				return tool.TextResult("タイムアウトを解除しました"), nil
			}
			return tool.TextResult(fmt.Sprintf("メンバーを%d分間タイムアウトしました", in.Minutes)), nil
		},
	}
}

func newDiscordListMembers(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_list_members",
		desc:    "サーバーのメンバー一覧を見る（最大100人）。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"limit":    {"type": "integer", "description": "Max members to return (1-100, default 50)."}
			},
			"required": ["guild_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				Limit   int    `json:"limit"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if in.Limit <= 0 || in.Limit > 100 {
				in.Limit = 50
			}
			members, err := s.GuildMembers(in.GuildID, "", in.Limit)
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
		},
	}
}

// ───────────────────────────────────────────────
// Role management
// ───────────────────────────────────────────────

func newDiscordAddRole(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_add_role",
		desc:    "メンバーにロールを付与する。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID."},
				"role_id":  {"type": "string", "description": "The role ID to add."}
			},
			"required": ["guild_id", "user_id", "role_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				UserID  string `json:"user_id"`
				RoleID  string `json:"role_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.GuildMemberRoleAdd(in.GuildID, in.UserID, in.RoleID); err != nil {
				return tool.ErrorResult("ロール付与失敗: " + err.Error()), nil
			}
			return tool.TextResult("ロールを付与しました"), nil
		},
	}
}

func newDiscordRemoveRole(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_remove_role",
		desc:    "メンバーからロールを外す。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID."},
				"role_id":  {"type": "string", "description": "The role ID to remove."}
			},
			"required": ["guild_id", "user_id", "role_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				UserID  string `json:"user_id"`
				RoleID  string `json:"role_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.GuildMemberRoleRemove(in.GuildID, in.UserID, in.RoleID); err != nil {
				return tool.ErrorResult("ロール削除失敗: " + err.Error()), nil
			}
			return tool.TextResult("ロールを削除しました"), nil
		},
	}
}

func newDiscordListRoles(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_list_roles",
		desc:    "サーバーのロール一覧を見る。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."}
			},
			"required": ["guild_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			roles, err := s.GuildRoles(in.GuildID)
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
		},
	}
}

// ───────────────────────────────────────────────
// Server info & management
// ───────────────────────────────────────────────

func newDiscordServerInfo(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_server_info",
		desc:    "サーバーの基本情報を見る（名前、人数、オーナーなど）。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."}
			},
			"required": ["guild_id"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			g, err := s.Guild(in.GuildID)
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
		},
	}
}

func newDiscordRenameServer(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_rename_server",
		desc:    "サーバー名を変更する。Manage Server権限が必要。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"name":     {"type": "string", "description": "New server name."}
			},
			"required": ["guild_id", "name"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID string `json:"guild_id"`
				Name    string `json:"name"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if in.Name == "" {
				return tool.ErrorResult("サーバー名は空にできません"), nil
			}
			params := discordgo.GuildParams{Name: in.Name}
			_, err := s.GuildEdit(in.GuildID, &params)
			if err != nil {
				return tool.ErrorResult("サーバー名変更失敗: " + err.Error()), nil
			}
			return tool.TextResult("サーバー名を「" + in.Name + "」に変更しました"), nil
		},
	}
}

func newDiscordSetNickname(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_set_nickname",
		desc:    "サーバー内のメンバーのニックネームを変更する。空文字でリセット。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"guild_id": {"type": "string", "description": "The server (guild) ID."},
				"user_id":  {"type": "string", "description": "The user ID whose nickname to change."},
				"nickname": {"type": "string", "description": "New nickname. Empty string to reset to username."}
			},
			"required": ["guild_id", "user_id", "nickname"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				GuildID  string `json:"guild_id"`
				UserID   string `json:"user_id"`
				Nickname string `json:"nickname"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			if err := s.GuildMemberNickname(in.GuildID, in.UserID, in.Nickname); err != nil {
				return tool.ErrorResult("ニックネーム変更失敗: " + err.Error()), nil
			}
			if in.Nickname == "" {
				return tool.TextResult("ニックネームをリセットしました"), nil
			}
			return tool.TextResult("ニックネームを「" + in.Nickname + "」に変更しました"), nil
		},
	}
}

// ───────────────────────────────────────────────
// Thread management
// ───────────────────────────────────────────────

func newDiscordCreateThread(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_create_thread",
		desc:    "チャンネルにスレッドを作る。既存のメッセージに紐付けることもできる。",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_id": {"type": "string", "description": "The parent channel ID."},
				"name":       {"type": "string", "description": "Thread name."},
				"message_id": {"type": "string", "description": "Message ID to start the thread from (optional). Omit for standalone thread."}
			},
			"required": ["channel_id", "name"]
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				ChannelID string `json:"channel_id"`
				Name      string `json:"name"`
				MessageID string `json:"message_id"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}
			var ch *discordgo.Channel
			var err error
			if in.MessageID != "" {
				ch, err = s.MessageThreadStartComplex(in.ChannelID, in.MessageID, &discordgo.ThreadStart{
					Name: in.Name,
					Type: discordgo.ChannelTypeGuildPublicThread,
				})
			} else {
				ch, err = s.ThreadStartComplex(in.ChannelID, &discordgo.ThreadStart{
					Name: in.Name,
					Type: discordgo.ChannelTypeGuildPublicThread,
				})
			}
			if err != nil {
				return tool.ErrorResult("スレッド作成失敗: " + err.Error()), nil
			}
			out, _ := json.Marshal(map[string]string{"id": ch.ID, "name": ch.Name})
			return tool.TextResult(string(out)), nil
		},
	}
}

// ───────────────────────────────────────────────
// Bot presence / status
// ───────────────────────────────────────────────

func newDiscordUpdateStatus(s *discordgo.Session) tool.Tool {
	return &discordTool{
		session: s,
		name:    "discord_update_status",
		desc:    "Botの表示状態とアクティビティを変更する。気分や行動に合わせて自由に設定できる。",
		schema: json.RawMessage(`{
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
		}`),
		fn: func(s *discordgo.Session, _ context.Context, input json.RawMessage) (*tool.ToolResult, error) {
			in, errRes := unmarshal[struct {
				Status       string `json:"status"`
				ActivityType string `json:"activity_type"`
				ActivityText string `json:"activity_text"`
			}](input)
			if errRes != nil {
				return errRes, nil
			}

			status := in.Status
			if status == "" {
				status = "online"
			}

			data := discordgo.UpdateStatusData{Status: status}

			if in.ActivityText != "" {
				actType := discordgo.ActivityTypeGame
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

			if err := s.UpdateStatusComplex(data); err != nil {
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
		},
	}
}
