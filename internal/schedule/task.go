package schedule

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/scheduler"
)

// Task is a CronTask that sends due scheduled actions.
type Task struct{}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "schedule" }
func (t *Task) Description() string { return "期限到来のスケジュールメッセージを送信" }

func (t *Task) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, _ json.RawMessage) error {
	store := NewStore(cc.DB)
	now := time.Now()

	// Use wall clock in the scheduler's timezone, but store uses UTC.
	actions, err := store.FetchDue(ctx, now)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		return nil
	}

	cc.Logger.Info("schedule: processing due actions", slog.Int("count", len(actions)))

	for _, a := range actions {
		_, sendErr := cc.Notifier.Send(ctx, a.ChannelID, a.Content, "schedule")
		if sendErr != nil {
			// Don't mark executed — will retry on next run.
			cc.Logger.Warn("schedule: send failed, will retry",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
				slog.Any("error", sendErr),
			)
			continue
		}

		if markErr := store.MarkExecuted(ctx, a.ID, now); markErr != nil {
			cc.Logger.Error("schedule: mark executed failed",
				slog.String("id", a.ID),
				slog.Any("error", markErr),
			)
		}

		if a.CronExpr != "" {
			cc.Logger.Info("schedule: recurring action sent, rescheduled",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
			)
		} else {
			cc.Logger.Info("schedule: action sent",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
			)
		}
	}
	return nil
}
