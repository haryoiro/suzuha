package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// WebSpeaker is the interface for sending TTS to web clients.
type WebSpeaker interface {
	SpeakTextTo(ctx context.Context, text string, kind string) error
	// SpeakStreamTo は sentence チャネルから 1 文ずつ TTS 合成して送信する。
	SpeakStreamTo(ctx context.Context, sentences <-chan string, kind string) error
}

// WebSession is the Session implementation for web widget interactions.
type WebSession struct {
	agentCtx *Context
	speaker  WebSpeaker
	logger   *slog.Logger
}

// NewWebSession creates a new WebSession.
func NewWebSession(agentCtx *Context, speaker WebSpeaker, logger *slog.Logger) *WebSession {
	return &WebSession{
		agentCtx: agentCtx,
		speaker:  speaker,
		logger:   logger,
	}
}

func (s *WebSession) Source() SourceKey                { return SourceKeyWeb }
func (s *WebSession) Context() *Context                { return s.agentCtx }
func (s *WebSession) PersistKey() string               { return "web" }
func (s *WebSession) DirectiveConfig() DirectiveConfig { return webDirectiveConfig() }
func (s *WebSession) BeginTurn(p *Perception)          {} // no turn context needed

func (s *WebSession) Respond(ctx context.Context, text string) error {
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
func (s *WebSession) RespondStream(ctx context.Context, sentences <-chan string) error {
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
func (s *WebSession) IsVoiceReady() bool {
	return s.speaker != nil
}
