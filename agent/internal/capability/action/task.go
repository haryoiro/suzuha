package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler/notification"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Task は期限到来した予約アクションを送信する scheduler.CronTask 実装。
type Task struct {
	store        *Store
	llm          portllm.Client
	notifier     notification.Notifier
	systemPrompt string
	logger       *slog.Logger
}

// NewTask は action Task を生成する。
func NewTask(db *sql.DB, llm portllm.Client, notifier notification.Notifier, systemPrompt string, logger *slog.Logger) *Task {
	return &Task{
		store:        NewStore(db),
		llm:          llm,
		notifier:     notifier,
		systemPrompt: systemPrompt,
		logger:       logger,
	}
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string { return "schedule" }
func (t *Task) Description() string {
	return "期限到来のスケジュールメッセージを送信"
}

// Setup は action には初期化不要。
func (t *Task) Setup(_ context.Context) error { return nil }

// Execute は期限到来したアクションを一括送信する。
func (t *Task) Execute(ctx context.Context, _ json.RawMessage) error {
	now := jtime.Now()

	actions, err := t.store.FetchDue(ctx, now)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		return nil
	}

	t.logger.Info("schedule: 期限到来のアクションを処理中", slog.Int("count", len(actions)))

	const maxRetries = 5

	for _, a := range actions {
		message := a.Content

		if a.Mode == "prompt" {
			generated, llmErr := t.generateFromPrompt(ctx, a.Content)
			if llmErr != nil {
				t.logger.Error("schedule: LLM生成に失敗、リトライします",
					slog.String("id", a.ID),
					slog.String("error", llmErr.Error()),
				)
				continue
			}
			message = generated
		}

		_, sendErr := t.notifier.Send(ctx, a.ChannelID, message, "schedule")
		if sendErr != nil {
			newCount := a.RetryCount + 1
			if newCount >= maxRetries {
				t.logger.Error("schedule: リトライ上限に到達、failedにマーク",
					slog.String("id", a.ID),
					slog.String("channel", a.ChannelID),
					slog.String("error", sendErr.Error()),
					slog.Int("retries", newCount),
				)
				if markErr := t.store.MarkFailed(ctx, a.ID, newCount); markErr != nil {
					t.logger.Error("schedule: failed状態への更新に失敗",
						slog.String("id", a.ID),
						slog.Any("error", markErr),
					)
				}
			} else {
				t.logger.Warn("schedule: 送信に失敗、リトライします",
					slog.String("id", a.ID),
					slog.String("channel", a.ChannelID),
					slog.String("error", sendErr.Error()),
					slog.Int("retry", newCount),
					slog.Int("max", maxRetries),
				)
				if retryErr := t.store.IncrRetry(ctx, a.ID, newCount); retryErr != nil {
					t.logger.Error("schedule: リトライカウント更新に失敗",
						slog.String("id", a.ID),
						slog.Any("error", retryErr),
					)
				}
			}
			continue
		}

		if markErr := t.store.MarkExecuted(ctx, a.ID, now); markErr != nil {
			t.logger.Error("schedule: 実行済みマークに失敗",
				slog.String("id", a.ID),
				slog.Any("error", markErr),
			)
		}

		if a.CronExpr != "" {
			t.logger.Info("schedule: 定期アクションを送信、再スケジュール済み",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
			)
		} else {
			t.logger.Info("schedule: アクションを送信済み",
				slog.String("id", a.ID),
				slog.String("channel", a.ChannelID),
			)
		}
	}
	return nil
}

func (t *Task) generateFromPrompt(ctx context.Context, prompt string) (string, error) {
	messages := []providers.Message{
		{Role: "user", Content: prompt},
	}
	if t.systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: t.systemPrompt}}, messages...)
	}

	resp, err := t.llm.For("background").CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}

	text := message.StripDirectiveTags(resp.Text)
	if text == "" {
		return "", fmt.Errorf("LLMが空またはサイレントなレスポンスを返しました")
	}
	return text, nil
}
