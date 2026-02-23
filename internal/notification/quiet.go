package notification

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// QuietHoursConfig defines the quiet hours window.
type QuietHoursConfig struct {
	Start    string // "HH:MM" format, e.g. "23:00"
	End      string // "HH:MM" format, e.g. "08:00"
	Location *time.Location
}

// WithQuietHours wraps a NotifyFunc to suppress notifications during quiet hours.
// Notifications attempted during quiet hours are silently dropped with a log message.
func WithQuietHours(inner NotifyFunc, cfg QuietHoursConfig, logger *slog.Logger) NotifyFunc {
	startH, startM, err := parseHHMM(cfg.Start)
	if err != nil {
		logger.Warn("quiet_hours: invalid start time, quiet hours disabled", "start", cfg.Start, "error", err)
		return inner
	}
	endH, endM, err := parseHHMM(cfg.End)
	if err != nil {
		logger.Warn("quiet_hours: invalid end time, quiet hours disabled", "end", cfg.End, "error", err)
		return inner
	}

	loc := cfg.Location
	if loc == nil {
		loc = time.UTC
	}

	return func(ctx context.Context, channelID, content, source string) error {
		now := time.Now().In(loc)
		if inQuietWindow(now, startH, startM, endH, endM) {
			logger.Info("quiet_hours: notification suppressed",
				"source", source,
				"channel", channelID,
				"time", now.Format("15:04"),
				"window", fmt.Sprintf("%s-%s", cfg.Start, cfg.End),
			)
			return nil
		}
		return inner(ctx, channelID, content, source)
	}
}

// inQuietWindow checks if the given time falls within the quiet window.
// Handles overnight windows (e.g. 23:00-08:00) correctly.
func inQuietWindow(now time.Time, startH, startM, endH, endM int) bool {
	nowMin := now.Hour()*60 + now.Minute()
	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin <= endMin {
		// Same-day window: e.g. 09:00-17:00
		return nowMin >= startMin && nowMin < endMin
	}
	// Overnight window: e.g. 23:00-08:00
	return nowMin >= startMin || nowMin < endMin
}

// parseHHMM parses "HH:MM" into hour and minute.
func parseHHMM(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("expected HH:MM format: %w", err)
	}
	return t.Hour(), t.Minute(), nil
}
