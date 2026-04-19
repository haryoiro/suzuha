package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/adapter/stt"
	"github.com/haryoiro/suzuha/internal/adapter/tts"
	"github.com/haryoiro/suzuha/internal/capability/voice"
	"github.com/haryoiro/suzuha/internal/port/tool"
)

// VoicePipeline returns the voice pipeline, creating it if necessary.
// Returns nil if voice is not configured.
func (c *Chat) VoicePipeline() *voice.Pipeline {
	return c.voicePipeline
}

// SetupVoice initializes the voice pipeline with STT and TTS clients.
// Must be called after Discord session is established (in OnReady).
func (c *Chat) SetupVoice(sttClient stt.STT, ttsClient tts.TTS) {
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
	session         *discordgo.Session
	allowedChannels map[string]struct{}
	logger          *slog.Logger
}

// NewVoiceJoin creates a voice_join tool. allowedChannels limits which VC channels
// can be joined (empty = allow all).
func NewVoiceJoin(pipeline *voice.Pipeline, session *discordgo.Session, allowedChannels []string, logger *slog.Logger) tool.Tool {
	allowed := make(map[string]struct{}, len(allowedChannels))
	for _, ch := range allowedChannels {
		allowed[ch] = struct{}{}
	}
	return &voiceJoinTool{pipeline: pipeline, session: session, allowedChannels: allowed, logger: logger}
}

func (t *voiceJoinTool) Name() string   { return "voice_join" }
func (t *voiceJoinTool) ReadOnly() bool { return false }
func (t *voiceJoinTool) Description() string {
	return "ボイスチャンネルに参加する。ユーザーに「VCに来て」と言われたときに使う。user_idを指定するとそのユーザーがいるVCに自動参加する。"
}
func (t *voiceJoinTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"guild_id": {"type": "string", "description": "サーバーID"},
			"channel_id": {"type": "string", "description": "ボイスチャンネルID（省略時はuser_idからVCを自動検出）"},
			"user_id": {"type": "string", "description": "参加先を自動検出するためのユーザーID（そのユーザーが今いるVCに参加する）"}
		},
		"required": ["guild_id"]
	}`)
}
func (t *voiceJoinTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var params struct {
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.ErrorResult(fmt.Sprintf("パラメータの解析に失敗: %v", err)), nil
	}
	if params.GuildID == "" {
		return tool.ErrorResult("guild_id は必須です"), nil
	}

	// channel_id が未指定の場合、user_id からVCを自動検出する
	if params.ChannelID == "" && params.UserID != "" {
		params.ChannelID = FindUserVoiceChannel(t.session, params.GuildID, params.UserID)
		if params.ChannelID == "" {
			return tool.ErrorResult(fmt.Sprintf("ユーザー %s はどのVCにもいません", params.UserID)), nil
		}
		t.logger.Info("voice: ユーザーのVCを自動検出", "user_id", params.UserID, "channel_id", params.ChannelID)
	}

	// どちらも未指定なら、VCにいる人がいるチャンネルを探すか、一覧を返す
	if params.ChannelID == "" {
		vcs, err := ListVoiceChannels(t.session, params.GuildID)
		if err != nil {
			return tool.ErrorResult(fmt.Sprintf("VCチャンネル一覧の取得に失敗: %v", err)), nil
		}
		// 人がいるVCを優先的に選ぶ
		g, _ := t.session.State.Guild(params.GuildID)
		if g != nil {
			occupiedChannels := make(map[string]int)
			for _, vs := range g.VoiceStates {
				occupiedChannels[vs.ChannelID]++
			}
			for _, vc := range vcs {
				if occupiedChannels[vc.ID] > 0 {
					params.ChannelID = vc.ID
					t.logger.Info("voice: 人がいるVCを自動選択", "channel_id", vc.ID, "channel_name", vc.Name)
					break
				}
			}
		}
		if params.ChannelID == "" {
			var names []string
			for _, vc := range vcs {
				names = append(names, fmt.Sprintf("%s (%s)", vc.Name, vc.ID))
			}
			return tool.ErrorResult(fmt.Sprintf("channel_id または user_id を指定してください。利用可能なVC: %v", names)), nil
		}
	}

	// channel_id がテキストチャンネルでないか検証
	ch, err := t.session.Channel(params.ChannelID)
	if err == nil && ch.Type != discordgo.ChannelTypeGuildVoice && ch.Type != discordgo.ChannelTypeGuildStageVoice {
		return tool.ErrorResult(fmt.Sprintf("チャンネル %s はボイスチャンネルではありません（type=%d）。テキストチャンネルIDではなくボイスチャンネルIDを指定してください", params.ChannelID, ch.Type)), nil
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

func (t *voiceLeaveTool) Name() string   { return "voice_leave" }
func (t *voiceLeaveTool) ReadOnly() bool { return false }
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
