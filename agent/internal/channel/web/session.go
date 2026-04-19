package web

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/agent"
)

// Speaker は web クライアントへ TTS を送る契約 (consumer-side interface)。
// device.Hub などが実装する。
type Speaker interface {
	SpeakTextTo(ctx context.Context, text string, kind string) error
	// SpeakStreamTo は sentence チャネルから 1 文ずつ TTS 合成して送信する。
	SpeakStreamTo(ctx context.Context, sentences <-chan string, kind string) error
}

// Session は web ウィジェット対話の agent.Session 実装。
type Session struct {
	agentCtx *agent.Context
	speaker  Speaker
	logger   *slog.Logger
}

// NewSession creates a new web Session.
func NewSession(agentCtx *agent.Context, speaker Speaker, logger *slog.Logger) *Session {
	return &Session{
		agentCtx: agentCtx,
		speaker:  speaker,
		logger:   logger,
	}
}

func (s *Session) Source() agent.SourceKey                { return agent.SourceKeyWeb }
func (s *Session) Context() *agent.Context                { return s.agentCtx }
func (s *Session) PersistKey() string                     { return "web" }
func (s *Session) DirectiveConfig() agent.DirectiveConfig { return agent.WebDirectiveConfig() }
func (s *Session) BeginTurn(*agent.Perception)            {} // no turn context needed

func (s *Session) Respond(ctx context.Context, text string) error {
	if s.speaker == nil {
		s.logger.Warn("Webスピーカーが設定されていない")
		return nil
	}
	s.logger.Info("Webクライアントに声で返す", "length", len(text))
	if err := s.speaker.SpeakTextTo(ctx, text, "web"); err != nil {
		return fmt.Errorf("speaking to web client: %w", err)
	}
	return nil
}

// RespondStream は sentence チャネルから逐次 TTS → WebSocket binary frame 送信する。
func (s *Session) RespondStream(ctx context.Context, sentences <-chan string) error {
	if s.speaker == nil {
		s.logger.Warn("Webスピーカーが設定されていない、sentences を破棄")
		for range sentences {
		}
		return nil
	}
	s.logger.Info("Webクライアントにストリーミングで返す")
	if err := s.speaker.SpeakStreamTo(ctx, sentences, "web"); err != nil {
		return fmt.Errorf("streaming to web client: %w", err)
	}
	return nil
}

// IsVoiceReady は Web クライアントが接続されてるか簡易確認する。
// speaker が設定されていれば TTS 送信は試みる (接続中かは Hub 側で判定)。
func (s *Session) IsVoiceReady() bool {
	return s.speaker != nil
}
