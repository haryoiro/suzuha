package diary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// HourlyTask creates hourly digest memories summarizing the past hour.
type HourlyTask struct{}

var _ scheduler.CronTask = (*HourlyTask)(nil)

func (t *HourlyTask) Name() string        { return "diary_hourly" }
func (t *HourlyTask) Description() string { return "1時間ごとの出来事を記録する" }

func (t *HourlyTask) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

func (t *HourlyTask) Execute(ctx context.Context, cc *scheduler.CronContext, _ json.RawMessage) error {
	now := jtime.Now()
	windowEnd := now.Truncate(time.Hour)
	windowStart := windowEnd.Add(-time.Hour)

	// 1. Collect conversation logs from the past hour.
	convLogs := fetchConversationLogs(ctx, cc, windowStart, windowEnd)

	// 2. Collect new memories from the past hour.
	recentMems := fetchRecentMemories(ctx, cc, windowStart)

	// 3. Collect memos from the past hour.
	recentMemos := fetchRecentMemos(ctx, cc, windowStart)

	// Nothing happened → skip.
	if len(convLogs) == 0 && len(recentMems) == 0 && len(recentMemos) == 0 {
		cc.Logger.Debug("diary_hourly: 何もなかったのでスキップ",
			"window_start", windowStart, "window_end", windowEnd)
		return nil
	}

	// 4. Fetch previous hourly digests for dedup context.
	prevDigests := fetchPreviousDigests(ctx, cc, windowStart)

	// 5. Build LLM prompt and summarize.
	localStart := jtime.In(windowStart)
	summary, err := summarizeHour(ctx, cc.LLM, cc.SystemPrompt, localStart, convLogs, recentMems, recentMemos, prevDigests)
	if err != nil {
		cc.Logger.Error("diary_hourly: 要約に失敗", "error", err)
		return err
	}

	// 5. Save to diary_entries table (not memories).
	ds := NewStore(cc.DB)
	entry := &Entry{
		Kind:        "hourly",
		Content:     summary,
		PeriodStart: windowStart,
		PeriodEnd:   windowEnd,
	}
	if err := ds.Save(ctx, entry); err != nil {
		cc.Logger.Error("diary_hourly: 日記保存に失敗", "error", err)
		return err
	}

	cc.Logger.Info("diary_hourly: 記録した",
		"period", localStart.Format("2006-01-02T15:00"),
		"conv_logs", len(convLogs), "memories", len(recentMems))
	return nil
}

// convLogRow represents a single row from conversation_logs.
type convLogRow struct {
	SourceKey   string
	ChannelID   string
	ChannelName string // populated from context messages if available
	Role        string
	UserName    string
	Content     string
	TS          time.Time
}

// sectionKey groups conversation logs into sections.
type sectionKey struct {
	SourceKey string
	ChannelID string
}

func fetchConversationLogs(ctx context.Context, cc *scheduler.CronContext, from, to time.Time) []convLogRow {
	if cc.DB == nil {
		return nil
	}
	rows, err := cc.DB.QueryContext(ctx,
		`SELECT source_key, channel_id, role, COALESCE(user_name, ''), content, timestamp
		 FROM conversation_logs
		 WHERE timestamp >= $1 AND timestamp < $2
		   AND role IN ('user', 'assistant')
		 ORDER BY timestamp ASC`,
		from, to,
	)
	if err != nil {
		cc.Logger.Debug("diary_hourly: conversation_logs query", "error", err)
		return nil
	}
	defer rows.Close()

	var result []convLogRow
	for rows.Next() {
		var r convLogRow
		var ts string
		if err := rows.Scan(&r.SourceKey, &r.ChannelID, &r.Role, &r.UserName, &r.Content, &ts); err != nil {
			continue
		}
		var parseErr error
		r.TS, parseErr = time.Parse(time.RFC3339, ts)
		if parseErr != nil {
			cc.Logger.Debug("diary_hourly: timestamp parse failed", "ts", ts, "error", parseErr)
		}
		result = append(result, r)
	}
	return result
}

func fetchRecentMemories(ctx context.Context, cc *scheduler.CronContext, since time.Time) []memory.Memory {
	if cc.Memory == nil {
		return nil
	}
	var all []memory.Memory
	for _, mt := range []memory.MemoryType{memory.MemoryTypeEpisode, memory.MemoryTypeUser, memory.MemoryTypeWorld} {
		mems, err := cc.Memory.ListRecentByType(ctx, mt, since, 20)
		if err != nil {
			cc.Logger.Debug("diary_hourly: list recent", "type", mt, "error", err)
			continue
		}
		all = append(all, mems...)
	}
	return all
}

func fetchRecentMemos(ctx context.Context, cc *scheduler.CronContext, since time.Time) []memory.Memory {
	if cc.Memory == nil {
		return nil
	}
	mems, err := cc.Memory.ListRecentByType(ctx, memory.MemoryTypeMemo, since, 20)
	if err != nil {
		cc.Logger.Debug("diary_hourly: list recent memos", "error", err)
		return nil
	}
	return mems
}

// fetchPreviousDigests returns the last few hourly digests before the given window.
func fetchPreviousDigests(ctx context.Context, cc *scheduler.CronContext, windowStart time.Time) []Entry {
	if cc.DB == nil {
		return nil
	}
	ds := NewStore(cc.DB)
	lookback := windowStart.Add(-3 * time.Hour)
	entries, err := ds.ListByKind(ctx, "hourly", lookback, 10)
	if err != nil {
		return nil
	}

	// Only include digests before the current window.
	var digests []Entry
	for _, e := range entries {
		if e.PeriodStart.Before(windowStart) {
			digests = append(digests, e)
			if len(digests) >= 2 {
				break
			}
		}
	}
	return digests
}

// groupLogsBySections groups conversation logs by source_key + channel_id,
// preserving the order of first appearance.
func groupLogsBySections(logs []convLogRow) ([]sectionKey, map[sectionKey][]convLogRow) {
	groups := make(map[sectionKey][]convLogRow)
	var order []sectionKey
	seen := make(map[sectionKey]bool)

	for _, l := range logs {
		sk := sectionKey{SourceKey: l.SourceKey, ChannelID: l.ChannelID}
		if !seen[sk] {
			seen[sk] = true
			order = append(order, sk)
		}
		groups[sk] = append(groups[sk], l)
	}
	return order, groups
}

// sectionHeading builds a human-readable heading for a section.
func sectionHeading(sk sectionKey) string {
	switch sk.SourceKey {
	case "device":
		return "Device"
	case "web":
		return "Web"
	default:
		// Discord: show channel ID (best we have without channel_name in logs).
		if sk.ChannelID != "" {
			return fmt.Sprintf("Discord <#%s>", sk.ChannelID)
		}
		return "Discord"
	}
}

func summarizeHour(ctx context.Context, llmClient *llm.Client, systemPrompt string, localStart time.Time, logs []convLogRow, mems []memory.Memory, memos []memory.Memory, prevDigests []Entry) (string, error) {
	var sb strings.Builder

	sb.WriteString("以下はこの1時間の出来事です。日記の一節として主観的に2〜3文で要約してください。\n")
	sb.WriteString("何時頃に何があったかわかるように書いてください。\n")
	sb.WriteString("「前の記録」と被る内容は繰り返さず、新しいことだけ書いてください。\n")
	sb.WriteString("何もなければ「特に何もなかった」でOKです。\n\n")

	fmt.Fprintf(&sb, "時間帯: %s ～ %s\n\n",
		localStart.Format("2006-01-02 15:04"),
		localStart.Add(time.Hour).Format("15:04"))

	// Previous digests for dedup.
	if len(prevDigests) > 0 {
		sb.WriteString("## 前の記録（被らないように）\n")
		for _, d := range prevDigests {
			fmt.Fprintf(&sb, "- [%s] %s\n", d.PeriodStart.Format("2006-01-02T15:00"), d.Content)
		}
		sb.WriteString("\n")
	}

	// Conversation logs grouped by source/channel.
	if len(logs) > 0 {
		order, groups := groupLogsBySections(logs)
		for _, sk := range order {
			section := groups[sk]
			fmt.Fprintf(&sb, "## %s\n", sectionHeading(sk))
			for _, l := range section {
				name := l.UserName
				if name == "" {
					name = l.Role
				}
				content := textutil.TruncateRunes(l.Content, 200)
				fmt.Fprintf(&sb, "- [%s] %s: %s\n", jtime.In(l.TS).Format("15:04"), name, content)
			}
			sb.WriteString("\n")
		}
	}

	// Memos.
	if len(memos) > 0 {
		sb.WriteString("## メモ\n")
		for _, m := range memos {
			ts := jtime.In(m.CreatedAt).Format("15:04")
			fmt.Fprintf(&sb, "- [%s] %s\n", ts, textutil.TruncateRunes(m.Content, 200))
		}
		sb.WriteString("\n")
	}

	// New memories.
	if len(mems) > 0 {
		sb.WriteString("## 新しい記憶\n")
		for _, m := range mems {
			fmt.Fprintf(&sb, "- [%s] %s\n", string(m.Type), textutil.TruncateRunes(m.Content, 200))
		}
		sb.WriteString("\n")
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

