package builtin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/port/tool"
)

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
		session:  s,
		name:     "discord_get_history",
		readOnly: true,
		desc:     "チャンネルの最近の会話を見て、流れを把握する。",
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
					Time:     m.Timestamp.Format(time.RFC3339),
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
