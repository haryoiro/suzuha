package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// WebSpeaker is the interface for sending TTS to web clients.
type WebSpeaker interface {
	SpeakTextTo(ctx context.Context, text string, kind string) error
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
