package builtin

import (
	"context"
	"encoding/json"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
)

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
		session:  s,
		name:     "discord_list_channels",
		readOnly: true,
		desc:     "サーバーのチャンネル一覧を見る。",
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
