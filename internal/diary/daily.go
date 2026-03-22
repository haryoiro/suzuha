package diary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/mozilla-ai/any-llm-go/providers"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
)

// DailyTask creates a daily diary from hourly digests.
type DailyTask struct{}

var _ scheduler.CronTask = (*DailyTask)(nil)

func (t *DailyTask) Name() string        { return "diary_daily" }
func (t *DailyTask) Description() string { return "1日の日記をまとめる" }

func (t *DailyTask) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

func (t *DailyTask) Execute(ctx context.Context, cc *scheduler.CronContext, _ json.RawMessage) error {
	now := jtime.Now()
	// Summarize the previous day (run at midnight → yesterday).
	yesterday := now.Add(-24 * time.Hour)
	localYesterday := jtime.In(yesterday)
	dayStart := time.Date(localYesterday.Year(), localYesterday.Month(), localYesterday.Day(), 0, 0, 0, 0, localYesterday.Location())

	// Collect hourly digests from yesterday.
	digests := fetchHourlyDigests(ctx, cc, dayStart)
	if len(digests) == 0 {
		cc.Logger.Debug("diary_daily: hourly digest がないのでスキップ",
			"date", dayStart.Format("2006-01-02"))
		return nil
	}

	// LLM summarize.
	dateStr := dayStart.Format("2006-01-02")
	summary, err := summarizeDay(ctx, cc.LLM, cc.SystemPrompt, dateStr, digests)
	if err != nil {
		cc.Logger.Error("diary_daily: 要約に失敗", "error", err)
		return err
	}

	// Save as self memory.
	mem := memory.Memory{
		Type:    memory.MemoryTypeSelf,
		Content: summary,
		Metadata: map[string]any{
			"kind": "daily_diary",
			"date": dateStr,
		},
	}
	if err := cc.Memory.Save(ctx, &mem); err != nil {
		cc.Logger.Error("diary_daily: メモリ保存に失敗", "error", err)
		return err
	}

	cc.Logger.Info("diary_daily: 日記を書いた",
		"date", dateStr, "hourly_count", len(digests))
	return nil
}

func fetchHourlyDigests(ctx context.Context, cc *scheduler.CronContext, dayStart time.Time) []memory.Memory {
	if cc.Memory == nil {
		return nil
	}
	// Get all self memories from the day.
	since := dayStart.UTC()
	mems, err := cc.Memory.ListRecentByType(ctx, memory.MemoryTypeSelf, since, 50)
	if err != nil {
		cc.Logger.Debug("diary_daily: list hourly digests", "error", err)
		return nil
	}

	// Filter to hourly_digest kind and within the target day.
	dayEnd := dayStart.Add(24 * time.Hour)
	var digests []memory.Memory
	for _, m := range mems {
		if m.Metadata == nil {
			continue
		}
		kind, _ := m.Metadata["kind"].(string)
		if kind != "hourly_digest" {
			continue
		}
		if m.CreatedAt.Before(since) || m.CreatedAt.After(dayEnd.UTC()) {
			continue
		}
		digests = append(digests, m)
	}

	// Sort chronologically (ListRecentByType returns DESC).
	for i, j := 0, len(digests)-1; i < j; i, j = i+1, j-1 {
		digests[i], digests[j] = digests[j], digests[i]
	}
	return digests
}

func summarizeDay(ctx context.Context, llmClient *llm.Client, systemPrompt string, dateStr string, digests []memory.Memory) (string, error) {
	var sb strings.Builder

	sb.WriteString("以下は今日1日の時間ごとの記録です。1日を振り返って日記を書いてください。\n")
	sb.WriteString("主観的に、その日の全体的な雰囲気や印象的だったことを含めて。\n")
	sb.WriteString("5〜8文程度で。\n\n")

	fmt.Fprintf(&sb, "日付: %s\n\n", dateStr)

	for _, d := range digests {
		hour, _ := d.Metadata["hour"].(string)
		fmt.Fprintf(&sb, "### %s\n%s\n\n", hour, d.Content)
	}

	var messages []providers.Message
	if systemPrompt != "" {
		messages = append(messages, providers.Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, providers.Message{Role: "user", Content: sb.String()})

	resp, err := llmClient.CompleteRawDefault(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("LLM completion: %w", err)
	}
	return strings.TrimSpace(resp.Text), nil
}
