package agent

import (
	"context"
	"log/slog"
	"strings"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
)

// DiscordSession is the Session implementation for Discord text and voice interactions.
type DiscordSession struct {
	agentCtx     *Context
	chat         chat.Sender
	voice        chat.VoiceSpeaker
	chanSettings *channelpkg.Store
	drainWindow  time.Duration
	logger       *slog.Logger

	// Current turn context (set by BeginTurn, used by Respond)
	turnChannel string
	turnIsVoice bool
	turnIsDM    bool
	turnGuildID string
}

// NewDiscordSession creates a new DiscordSession.
func NewDiscordSession(
	agentCtx *Context,
	chatSender chat.Sender,
	voice chat.VoiceSpeaker,
	chanSettings *channelpkg.Store,
	drainWindow time.Duration,
	logger *slog.Logger,
) *DiscordSession {
	return &DiscordSession{
		agentCtx:     agentCtx,
		chat:         chatSender,
		voice:        voice,
		chanSettings: chanSettings,
		drainWindow:  drainWindow,
		logger:       logger,
	}
}

func (s *DiscordSession) Source() SourceKey          { return SourceKeyDiscord }
func (s *DiscordSession) Context() *Context          { return s.agentCtx }
func (s *DiscordSession) PersistKey() string         { return "discord" }
func (s *DiscordSession) DirectiveConfig() DirectiveConfig {
	return discordDirectiveConfig(s.drainWindow)
}

func (s *DiscordSession) BeginTurn(p *Perception) {
	s.turnChannel = p.Channel
	s.turnIsVoice = p.IsVoice
	s.turnIsDM = p.IsDM
	s.turnGuildID = p.LastEvent.Message.GuildID
}

func (s *DiscordSession) Respond(ctx context.Context, text string) error {
	// Suppress non-active channels (Discord-specific).
	if s.chanSettings != nil && s.turnChannel != "" && !s.turnIsDM {
		mode := s.chanSettings.GetMode(s.turnChannel)
		if mode != channelpkg.ModeActive {
			s.logger.Info("静かなチャンネルなので自重した",
				"channel", s.turnChannel, "mode", string(mode))
			return nil
		}
	}

	// Voice channel: speak, fallback to text on error.
	if s.voice != nil && s.turnGuildID != "" && s.voice.IsConnected(s.turnGuildID) {
		s.logger.Info("VCで声で返す", "guild", s.turnGuildID, "length", len(text))
		if err := s.voice.SpeakText(ctx, s.turnGuildID, text); err != nil {
			s.logger.Warn("VCの声が出なかったのでテキストで返す", "error", err)
			return s.chat.Send(ctx, s.turnChannel, text)
		}
		return nil
	}

	// Text channel.
	return s.chat.Send(ctx, s.turnChannel, text)
}

// RespondStream は LLM ストリーミングレスポンスを音声で逐次返す。
// voice が接続中ならストリーミング TTS、そうでなければテキスト送信にフォールバック。
func (s *DiscordSession) RespondStream(ctx context.Context, sentences <-chan string) error {
	// Voice channel: stream TTS.
	if s.voice != nil && s.turnGuildID != "" && s.voice.IsConnected(s.turnGuildID) {
		s.logger.Info("VCでストリーミング返答", "guild", s.turnGuildID)
		if err := s.voice.SpeakStream(ctx, s.turnGuildID, sentences); err != nil {
			s.logger.Warn("VCストリーミング失敗", "error", err)
			return err
		}
		return nil
	}

	// Text channel fallback: drain sentences and send as one message.
	var buf []string
	for sentence := range sentences {
		buf = append(buf, sentence)
	}
	if len(buf) == 0 {
		return nil
	}
	return s.chat.Send(ctx, s.turnChannel, strings.Join(buf, ""))
}

// Typing sends a typing indicator (Discord-specific, used during tool loops).
func (s *DiscordSession) Typing(ctx context.Context) {
	if s.turnChannel == "" {
		return
	}
	if typer, ok := s.chat.(chat.Typer); ok {
		typer.Typing(ctx, s.turnChannel)
	}
}

// SetVoice sets the voice speaker for voice channel responses.
func (s *DiscordSession) SetVoice(v chat.VoiceSpeaker) {
	s.voice = v
}

// IsVoiceReady は現在のターンが VC で、かつ voice 接続が生きているかを返す。
func (s *DiscordSession) IsVoiceReady() bool {
	return s.turnIsVoice && s.voice != nil && s.turnGuildID != "" && s.voice.IsConnected(s.turnGuildID)
}
