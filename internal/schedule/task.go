package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
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
	now := jtime.Now()

	// Use wall clock in the scheduler's timezone, but store uses UTC.
	actions, err := store.FetchDue(ctx, now)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		return nil
	}

	cc.Logger.Info("schedule: 期限到来のアクションを処理中", slog.Int("count", len(actions)))

	const maxRetries = 5

	for _, a := range actions {
		message := a.Content

		// In prompt mode, pass content through LLM to generate a natural response.
		if a.Mode == "prompt" {
			generated, llmErr := generateFromPrompt(ctx, cc, a.Content)
			if llmErr != nil {
				cc.Logger.Error("schedule: LLM生成に失敗、リトライします",
					slog.String("id", a.ID),
					slog.String("error", llmErr.Error()),
				)
				continue
			}
			message = generated
		}

		_, sendErr := cc.Notifier.Send(ctx, a.ChannelID, message, "schedule")
		if sendErr != nil {
			newCount := a.RetryCount + 1
			if newCount >= maxRetries {
				cc.Logger.Error("schedule: リトライ上限に到達、failedにマーク",
					slog.String("id", a.ID),
					slog.String("channel", a.ChannelID),
					slog.String("error", sendErr.Error()),
					slog.Int("retries", newCount),
				)
				_ = store.MarkFailed(ctx, a.ID, newCount)
			} else {
				cc.Logger.Warn("schedule: 送信に失敗、リトライします",
					slog.String("id", a.ID),
					slog.String("channel", a.ChannelID),
					slog.String("error", sendErr.Error()),
					slog.Int("retry", newCount),
					slog.Int("max", maxRetries),
				)
				_ = store.IncrRetry(ctx, a.ID, newCount)
			}
			continue
		}

		if markErr := store.MarkExecuted(ctx, a.ID, now); markErr != nil {
			cc.Logger.Error("schedule: 実行済みマークに失敗",
				slog.String("id", a.ID),
				slog.Any("error", markErr),
			)
		}

		if a.CronExpr != "" {
			cc.Logger.Info("schedule: 定期アクションを送信、再スケジュール済み",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
			)
		} else {
			cc.Logger.Info("schedule: アクションを送信済み",
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

	resp, err := cc.LLM.CompleteRawDefault(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}

	text := llm.StripDirectiveTags(resp.Text)
	if text == "" {
		return "", fmt.Errorf("LLMが空またはサイレントなレスポンスを返しました")
	}
	return text, nil
}
