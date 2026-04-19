package notification

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	domainchannel "github.com/haryoiro/suzuha/internal/domain/channel"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	portconv "github.com/haryoiro/suzuha/internal/port/conversation"
)

// Middleware wraps a Notifier with additional behavior.
type Middleware func(Notifier) Notifier

// QuietHoursConfig defines the quiet hours window.
type QuietHoursConfig struct {
	Start    string // "HH:MM" format, e.g. "23:00"
	End      string // "HH:MM" format, e.g. "08:00"
	Location *time.Location
}

// WithQuietHours returns middleware that suppresses all notifications (Send and Reply)
// during the configured quiet window. Suppressed messages return empty SendResult with nil error.
func WithQuietHours(cfg QuietHoursConfig, logger *slog.Logger) Middleware {
	startH, startM, err := parseHHMM(cfg.Start)
	if err != nil {
		logger.Warn("quiet_hours: 開始時刻が無効です。ミドルウェアを無効化します", "start", cfg.Start, "error", err)
		return func(n Notifier) Notifier { return n }
	}
	endH, endM, err := parseHHMM(cfg.End)
	if err != nil {
		logger.Warn("quiet_hours: 終了時刻が無効です。ミドルウェアを無効化します", "end", cfg.End, "error", err)
		return func(n Notifier) Notifier { return n }
	}

	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}

	return func(inner Notifier) Notifier {
		return &quietHoursNotifier{
			inner:  inner,
			loc:    loc,
			startH: startH, startM: startM,
			endH: endH, endM: endM,
			logger: logger,
			window: fmt.Sprintf("%s-%s", cfg.Start, cfg.End),
		}
	}
}

type quietHoursNotifier struct {
	inner          Notifier
	loc            *time.Location
	startH, startM int
	endH, endM     int
	logger         *slog.Logger
	window         string
}

func (q *quietHoursNotifier) Send(ctx context.Context, channelID, content, source string) (SendResult, error) {
	if q.suppressed(source, channelID) {
		return SendResult{}, nil
	}
	return q.inner.Send(ctx, channelID, content, source)
}

func (q *quietHoursNotifier) Reply(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error) {
	if q.suppressed(source, channelID) {
		return SendResult{}, nil
	}
	return q.inner.Reply(ctx, channelID, content, replyToID, source)
}

func (q *quietHoursNotifier) suppressed(source, channelID string) bool {
	now := jtime.Now()
	if inQuietWindow(now, q.startH, q.startM, q.endH, q.endM) {
		q.logger.Info("quiet_hours: 通知を抑制しました",
			"source", source,
			"channel", channelID,
			"time", now.Format("15:04"),
			"window", q.window,
		)
		return true
	}
	return false
}

// inQuietWindow checks if the given time falls within the quiet window.
// Handles overnight windows (e.g. 23:00-08:00) correctly.
func inQuietWindow(now time.Time, startH, startM, endH, endM int) bool {
	nowMin := now.Hour()*60 + now.Minute()
	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin <= endMin {
		return nowMin >= startMin && nowMin < endMin
	}
	return nowMin >= startMin || nowMin < endMin
}

// parseHHMM parses "HH:MM" into hour and minute.
func parseHHMM(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("HH:MM形式が必要です: %w", err)
	}
	return t.Hour(), t.Minute(), nil
}

// WithChannelSettings は channel_settings で disabled / listen のチャンネルへの
// 通知を抑制する middleware を返す。
func WithChannelSettings(settings portconv.SettingsStore, logger *slog.Logger) Middleware {
	return func(inner Notifier) Notifier {
		return &channelSettingsNotifier{inner: inner, settings: settings, logger: logger}
	}
}

type channelSettingsNotifier struct {
	inner    Notifier
	settings portconv.SettingsStore
	logger   *slog.Logger
}

func (n *channelSettingsNotifier) Send(ctx context.Context, channelID, content, source string) (SendResult, error) {
	if n.suppressed(channelID, source) {
		return SendResult{}, nil
	}
	return n.inner.Send(ctx, channelID, content, source)
}

func (n *channelSettingsNotifier) Reply(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error) {
	if n.suppressed(channelID, source) {
		return SendResult{}, nil
	}
	return n.inner.Reply(ctx, channelID, content, replyToID, source)
}

func (n *channelSettingsNotifier) suppressed(channelID, source string) bool {
	mode := n.settings.GetMode(channelID)
	if mode == domainchannel.ModeDisabled || mode == domainchannel.ModeListen {
		n.logger.Info("channel_settings: 通知を抑制しました",
			"channel", channelID, "mode", string(mode), "source", source)
		return true
	}
	return false
}
