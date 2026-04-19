package device

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/runtime/agent"
)

// Session は物理デバイス (ESP32) 対話の agent.Session 実装。
// Speaker 型は device.go で定義 (Hub が実装する契約)。
type Session struct {
	agentCtx *agent.Context
	speaker  Speaker
	logger   *slog.Logger
}

// NewSession creates a new device Session.
func NewSession(agentCtx *agent.Context, speaker Speaker, logger *slog.Logger) *Session {
	return &Session{
		agentCtx: agentCtx,
		speaker:  speaker,
		logger:   logger,
	}
}

func (s *Session) Source() agent.SourceKey                { return agent.SourceKeyDevice }
func (s *Session) Context() *agent.Context                { return s.agentCtx }
func (s *Session) PersistKey() string                     { return "device" }
func (s *Session) DirectiveConfig() agent.DirectiveConfig { return agent.DeviceDirectiveConfig() }
func (s *Session) BeginTurn(*agent.Perception)            {} // no turn context needed

func (s *Session) Respond(ctx context.Context, text string) error {
	if s.speaker == nil {
		s.logger.Warn("デバイスのスピーカーが設定されていない")
		return nil
	}
	s.logger.Info("デバイスに声で返す", "length", len(text))
	if err := s.speaker.SpeakText(ctx, text); err != nil {
		return fmt.Errorf("speaking to device: %w", err)
	}
	return nil
}
