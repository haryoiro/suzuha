package agent

import (
	"context"
	"log/slog"
)

// DeviceSession is the Session implementation for physical device (ESP32) interactions.
type DeviceSession struct {
	agentCtx *Context
	speaker  DeviceSpeaker
	logger   *slog.Logger
}

// NewDeviceSession creates a new DeviceSession.
func NewDeviceSession(agentCtx *Context, speaker DeviceSpeaker, logger *slog.Logger) *DeviceSession {
	return &DeviceSession{
		agentCtx: agentCtx,
		speaker:  speaker,
		logger:   logger,
	}
}

func (s *DeviceSession) Source() SourceKey              { return SourceKeyDevice }
func (s *DeviceSession) Context() *Context              { return s.agentCtx }
func (s *DeviceSession) PersistKey() string             { return "device" }
func (s *DeviceSession) DirectiveConfig() DirectiveConfig { return deviceDirectiveConfig() }
func (s *DeviceSession) BeginTurn(p *Perception)        {} // no turn context needed

func (s *DeviceSession) Respond(ctx context.Context, text string) error {
	if s.speaker == nil {
		s.logger.Warn("デバイスのスピーカーが設定されていない")
		return nil
	}
	s.logger.Info("デバイスに声で返す", "length", len(text))
	if err := s.speaker.SpeakText(ctx, text); err != nil {
		s.logger.Warn("デバイスへの声が届かなかった", "error", err)
		return err
	}
	return nil
}
