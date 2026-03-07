package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/voice"
)

// VoicePipeline returns the voice pipeline, creating it if necessary.
// Returns nil if voice is not configured.
func (c *Chat) VoicePipeline() *voice.Pipeline {
	return c.voicePipeline
}

// SetupVoice initializes the voice pipeline with STT and TTS clients.
// Must be called after Discord session is established (in OnReady).
func (c *Chat) SetupVoice(sttClient voice.STT, ttsClient voice.TTS) {
	if c.session == nil {
		c.log.Warn("voice: セッション未初期化のためセットアップをスキップ")
		return
	}
	c.voicePipeline = voice.NewPipeline(c.session, c.bus, sttClient, ttsClient, c.log)
	c.log.Info("voice: パイプラインをセットアップしました")
}

// voiceJoinTool allows the LLM to join a voice channel.
type voiceJoinTool struct {
	pipeline        *voice.Pipeline
	allowedChannels map[string]struct{}
	logger          *slog.Logger
}

// NewVoiceJoin creates a voice_join tool. allowedChannels limits which VC channels
// can be joined (empty = allow all).
func NewVoiceJoin(pipeline *voice.Pipeline, allowedChannels []string, logger *slog.Logger) tool.Tool {
	allowed := make(map[string]struct{}, len(allowedChannels))
	for _, ch := range allowedChannels {
		allowed[ch] = struct{}{}
	}
	return &voiceJoinTool{pipeline: pipeline, allowedChannels: allowed, logger: logger}
}

func (t *voiceJoinTool) Name() string { return "voice_join" }
func (t *voiceJoinTool) Description() string {
	return "ボイスチャンネルに参加する。ユーザーに「VCに来て」と言われたときに使う。"
}
func (t *voiceJoinTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "サーバーID"},
			"channel_id": {"type": "string", "description": "ボイスチャンネルID"}
		},
		"required": ["guild_id", "channel_id"]
	}`)
}
func (t *voiceJoinTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var params struct {
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.ErrorResult(fmt.Sprintf("パラメータの解析に失敗: %v", err)), nil
	}
	if params.GuildID == "" || params.ChannelID == "" {
		return tool.ErrorResult("guild_id と channel_id は必須です"), nil
	}

	if len(t.allowedChannels) > 0 {
		if _, ok := t.allowedChannels[params.ChannelID]; !ok {
			return tool.ErrorResult("このチャンネルでのVC参加は許可されていません"), nil
		}
	}

	if err := t.pipeline.Join(ctx, params.GuildID, params.ChannelID); err != nil {
		return tool.ErrorResult(fmt.Sprintf("VC参加に失敗: %v", err)), nil
	}
	t.logger.Info("voice: ツール経由でVC参加", "guild", params.GuildID, "channel", params.ChannelID)
	return tool.TextResult("ボイスチャンネルに参加しました"), nil
}

// voiceLeaveTool allows the LLM to leave a voice channel.
type voiceLeaveTool struct {
	pipeline *voice.Pipeline
	session  *discordgo.Session
	logger   *slog.Logger
}

func NewVoiceLeave(pipeline *voice.Pipeline, session *discordgo.Session, logger *slog.Logger) tool.Tool {
	return &voiceLeaveTool{pipeline: pipeline, session: session, logger: logger}
}

func (t *voiceLeaveTool) Name() string { return "voice_leave" }
func (t *voiceLeaveTool) Description() string {
	return "現在参加しているボイスチャンネルから離脱する。"
}
func (t *voiceLeaveTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "サーバーID"}
		},
		"required": ["guild_id"]
	}`)
}
func (t *voiceLeaveTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var params struct {
		GuildID string `json:"guild_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.ErrorResult(fmt.Sprintf("パラメータの解析に失敗: %v", err)), nil
	}
	if params.GuildID == "" {
		return tool.ErrorResult("guild_id は必須です"), nil
	}

	if err := t.pipeline.Leave(params.GuildID); err != nil {
		return tool.ErrorResult(fmt.Sprintf("VC離脱に失敗: %v", err)), nil
	}
	t.logger.Info("voice: ツール経由でVC離脱", "guild", params.GuildID)
	return tool.TextResult("ボイスチャンネルから離脱しました"), nil
}

// FindUserVoiceChannel looks up which voice channel a user is in within a guild.
func FindUserVoiceChannel(s *discordgo.Session, guildID, userID string) string {
	g, err := s.State.Guild(guildID)
	if err != nil {
		return ""
	}
	for _, vs := range g.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID
		}
	}
	return ""
}

// ListVoiceChannels returns all voice channels in a guild.
func ListVoiceChannels(s *discordgo.Session, guildID string) ([]*discordgo.Channel, error) {
	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}
	var voiceChannels []*discordgo.Channel
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildVoice || ch.Type == discordgo.ChannelTypeGuildStageVoice {
			voiceChannels = append(voiceChannels, ch)
		}
	}
	return voiceChannels, nil
}
