package diary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// DailyTask creates a daily diary from hourly digests.
type DailyTask struct{}

var _ scheduler.CronTask = (*DailyTask)(nil)

func (t *DailyTask) Name() string        { return "diary_daily" }
func (t *DailyTask) Description() string { return "1日の日記をまとめる" }

func (t *DailyTask) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

func (t *DailyTask) Execute(ctx context.Context, cc *scheduler.CronContext, _ json.RawMessage) error {
	now := jtime.Now()
	yesterday := now.Add(-24 * time.Hour)
	localYesterday := jtime.In(yesterday)
	dayStart := time.Date(localYesterday.Year(), localYesterday.Month(), localYesterday.Day(), 0, 0, 0, 0, localYesterday.Location())

	// diary_entries テーブルから hourly digest を取得。
	ds := NewStore(cc.DB)
	digests, err := ds.ListByKind(ctx, "hourly", dayStart, 50)
	if err != nil {
		cc.Logger.Debug("diary_daily: hourly digest 取得に失敗", "error", err)
		return nil
	}

	// 対象日のみフィルタし、時系列順にソート。
	dayEnd := dayStart.Add(24 * time.Hour)
	var filtered []Entry
	for _, e := range digests {
		if !e.PeriodStart.Before(dayStart) && e.PeriodStart.Before(dayEnd) {
			filtered = append(filtered, e)
		}
	}
	// ListByKind は DESC なので反転して時系列順に。
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}

	if len(filtered) == 0 {
		cc.Logger.Debug("diary_daily: hourly digest がないのでスキップ",
			"date", dayStart.Format("2006-01-02"))
		return nil
	}

	// LLM で日記を要約。
	dateStr := dayStart.Format("2006-01-02")
	summary, err := summarizeDay(ctx, cc.LLM, cc.SystemPrompt, dateStr, filtered)
	if err != nil {
		cc.Logger.Error("diary_daily: 要約に失敗", "error", err)
		return err
	}

	// diary_entries テーブルに保存。
	entry := &Entry{
		Kind:        "daily",
		Content:     summary,
		PeriodStart: dayStart,
		PeriodEnd:   dayEnd,
	}
	if err := ds.Save(ctx, entry); err != nil {
		cc.Logger.Error("diary_daily: 日記保存に失敗", "error", err)
		return err
	}

	cc.Logger.Info("diary_daily: 日記を書いた",
		"date", dateStr, "hourly_count", len(filtered))
	return nil
}

func summarizeDay(ctx context.Context, llmClient *llm.Client, systemPrompt string, dateStr string, digests []Entry) (string, error) {
	var sb strings.Builder

	sb.WriteString("以下は今日1日の時間ごとの記録です。1日を振り返って日記を書いてください。\n")
	sb.WriteString("主観的に、その日の全体的な雰囲気や印象的だったことを含めて。\n")
	sb.WriteString("5〜8文程度で。\n\n")

	fmt.Fprintf(&sb, "日付: %s\n\n", dateStr)

	for _, d := range digests {
		fmt.Fprintf(&sb, "### %s\n%s\n\n", d.PeriodStart.Format("2006-01-02T15:00"), d.Content)
	}

	var messages []providers.Message
	if systemPrompt != "" {
		messages = append(messages, providers.Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, providers.Message{Role: "user", Content: sb.String()})

	resp, err := llmClient.For("background").CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("LLM completion: %w", err)
	}
	return strings.TrimSpace(resp.Text), nil
}
