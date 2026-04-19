package discord

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/agent"
	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
)

// Session は Discord のテキスト / 音声対話に対応する agent.Session 実装。
type Session struct {
	agentCtx     *agent.Context
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

// NewSession creates a new Discord Session.
func NewSession(
	agentCtx *agent.Context,
	chatSender chat.Sender,
	voice chat.VoiceSpeaker,
	chanSettings *channelpkg.Store,
	drainWindow time.Duration,
	logger *slog.Logger,
) *Session {
	return &Session{
		agentCtx:     agentCtx,
		chat:         chatSender,
		voice:        voice,
		chanSettings: chanSettings,
		drainWindow:  drainWindow,
		logger:       logger,
	}
}

func (s *Session) Source() agent.SourceKey { return agent.SourceKeyDiscord }
func (s *Session) Context() *agent.Context { return s.agentCtx }
func (s *Session) PersistKey() string      { return "discord" }

func (s *Session) DirectiveConfig() agent.DirectiveConfig {
	return agent.DiscordDirectiveConfig(s.drainWindow)
}

func (s *Session) BeginTurn(p *agent.Perception) {
	s.turnChannel = p.Channel
	s.turnIsVoice = p.IsVoice
	s.turnIsDM = p.IsDM
	s.turnGuildID = p.LastEvent.Message.GuildID
}

func (s *Session) Respond(ctx context.Context, text string) error {
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
func (s *Session) RespondStream(ctx context.Context, sentences <-chan string) error {
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
func (s *Session) Typing(ctx context.Context) {
	if s.turnChannel == "" {
		return
	}
	if typer, ok := s.chat.(chat.Typer); ok {
		typer.Typing(ctx, s.turnChannel)
	}
}

// SetVoice sets the voice speaker for voice channel responses.
func (s *Session) SetVoice(v chat.VoiceSpeaker) {
	s.voice = v
}

// IsVoiceReady は現在のターンが VC で、かつ voice 接続が生きているかを返す。
func (s *Session) IsVoiceReady() bool {
	return s.turnIsVoice && s.voice != nil && s.turnGuildID != "" && s.voice.IsConnected(s.turnGuildID)
}
