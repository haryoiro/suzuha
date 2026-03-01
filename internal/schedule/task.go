package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
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
		message := a.Content

		// In prompt mode, pass content through LLM to generate a natural response.
		if a.Mode == "prompt" {
			generated, llmErr := generateFromPrompt(ctx, cc, a.Content)
			if llmErr != nil {
				cc.Logger.Error("schedule: llm generation failed, will retry",
					slog.String("id", a.ID),
					slog.Any("error", llmErr),
				)
				continue
			}
			message = generated
		}

		_, sendErr := cc.Notifier.Send(ctx, a.ChannelID, message, "schedule")
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

// generateFromPrompt sends the content as a prompt to the LLM and returns the response.
func generateFromPrompt(ctx context.Context, cc *scheduler.CronContext, prompt string) (string, error) {
	messages := []providers.Message{
		{Role: "user", Content: prompt},
	}
	if cc.SystemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: cc.SystemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return "", fmt.Errorf("llm returned empty response")
	}
	return text, nil
}
