package summarize

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// HourlyTask は 1 時間ごとの出来事を要約する scheduler.CronTask 実装。
type HourlyTask struct {
	db           *sql.DB
	llm          portllm.Client
	memory       portmem.Memory
	systemPrompt string
	logger       *slog.Logger
}

// NewHourlyTask は HourlyTask を生成する。
func NewHourlyTask(db *sql.DB, llm portllm.Client, mem portmem.Memory, systemPrompt string, logger *slog.Logger) *HourlyTask {
	return &HourlyTask{db: db, llm: llm, memory: mem, systemPrompt: systemPrompt, logger: logger}
}

var _ scheduler.CronTask = (*HourlyTask)(nil)

func (t *HourlyTask) Name() string        { return "diary_hourly" }
func (t *HourlyTask) Description() string { return "1時間ごとの出来事を記録する" }

// Setup は hourly には初期化不要。
func (t *HourlyTask) Setup(_ context.Context) error { return nil }

// Execute は直前 1 時間の会話と記憶を要約し diary_entries に保存する。
func (t *HourlyTask) Execute(ctx context.Context, _ json.RawMessage) error {
	now := jtime.Now()
	windowEnd := now.Truncate(time.Hour)
	windowStart := windowEnd.Add(-time.Hour)

	convLogs := t.fetchConversationLogs(ctx, windowStart, windowEnd)
	recentMems := t.fetchRecentMemories(ctx, windowStart)
	recentMemos := t.fetchRecentMemos(ctx, windowStart)

	if len(convLogs) == 0 && len(recentMems) == 0 && len(recentMemos) == 0 {
		t.logger.Debug("diary_hourly: 何もなかったのでスキップ",
			"window_start", windowStart, "window_end", windowEnd)
		return nil
	}

	prevDigests := t.fetchPreviousDigests(ctx, windowStart)

	localStart := jtime.In(windowStart)
	summary, err := summarizeHour(ctx, t.llm, t.systemPrompt, localStart, convLogs, recentMems, recentMemos, prevDigests)
	if err != nil {
		t.logger.Error("diary_hourly: 要約に失敗", "error", err)
		return err
	}

	ds := NewStore(t.db)
	entry := &Entry{
		Kind:        "hourly",
		Content:     summary,
		PeriodStart: windowStart,
		PeriodEnd:   windowEnd,
	}
	if err := ds.Save(ctx, entry); err != nil {
		t.logger.Error("diary_hourly: 日記保存に失敗", "error", err)
		return err
	}

	t.logger.Info("diary_hourly: 記録した",
		"period", localStart.Format("2006-01-02T15:00"),
		"conv_logs", len(convLogs), "memories", len(recentMems))
	return nil
}

// convLogRow は conversation_logs の 1 行を表す。
type convLogRow struct {
	SourceKey   string
	ChannelID   string
	ChannelName string
	Role        string
	UserName    string
	Content     string
	TS          time.Time
}

// sectionKey は会話ログをセクションにグルーピングするキー。
type sectionKey struct {
	SourceKey string
	ChannelID string
}

func (t *HourlyTask) fetchConversationLogs(ctx context.Context, from, to time.Time) []convLogRow {
	if t.db == nil {
		return nil
	}
	rows, err := t.db.QueryContext(ctx,
		`SELECT source_key, channel_id, role, COALESCE(user_name, ''), content, timestamp
		 FROM conversation_logs
		 WHERE timestamp >= $1 AND timestamp < $2
		   AND role IN ('user', 'assistant')
		 ORDER BY timestamp ASC`,
		from, to,
	)
	if err != nil {
		t.logger.Debug("diary_hourly: conversation_logs query", "error", err)
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
			t.logger.Debug("diary_hourly: timestamp parse failed", "ts", ts, "error", parseErr)
		}
		result = append(result, r)
	}
	return result
}

func (t *HourlyTask) fetchRecentMemories(ctx context.Context, since time.Time) []memory.Memory {
	if t.memory == nil {
		return nil
	}
	var all []memory.Memory
	for _, mt := range []memory.MemoryType{memory.MemoryTypeEpisode, memory.MemoryTypeUser, memory.MemoryTypeWorld} {
		mems, err := t.memory.ListRecentByType(ctx, mt, since, 20)
		if err != nil {
			t.logger.Debug("diary_hourly: list recent", "type", mt, "error", err)
			continue
		}
		all = append(all, mems...)
	}
	return all
}

func (t *HourlyTask) fetchRecentMemos(ctx context.Context, since time.Time) []memory.Memory {
	if t.memory == nil {
		return nil
	}
	mems, err := t.memory.ListRecentByType(ctx, memory.MemoryTypeMemo, since, 20)
	if err != nil {
		t.logger.Debug("diary_hourly: list recent memos", "error", err)
		return nil
	}
	return mems
}

func (t *HourlyTask) fetchPreviousDigests(ctx context.Context, windowStart time.Time) []Entry {
	if t.db == nil {
		return nil
	}
	ds := NewStore(t.db)
	lookback := windowStart.Add(-3 * time.Hour)
	entries, err := ds.ListByKind(ctx, "hourly", lookback, 10)
	if err != nil {
		return nil
	}

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

func sectionHeading(sk sectionKey) string {
	switch sk.SourceKey {
	case "device":
		return "Device"
	case "web":
		return "Web"
	default:
		if sk.ChannelID != "" {
			return fmt.Sprintf("Discord <#%s>", sk.ChannelID)
		}
		return "Discord"
	}
}

func summarizeHour(ctx context.Context, llmClient portllm.Client, systemPrompt string, localStart time.Time, logs []convLogRow, mems []memory.Memory, memos []memory.Memory, prevDigests []Entry) (string, error) {
	var sb strings.Builder

	sb.WriteString("以下はこの1時間の出来事です。日記の一節として主観的に2〜3文で要約してください。\n")
	sb.WriteString("何時頃に何があったかわかるように書いてください。\n")
	sb.WriteString("「前の記録」と被る内容は繰り返さず、新しいことだけ書いてください。\n")
	sb.WriteString("何もなければ「特に何もなかった」でOKです。\n\n")

	fmt.Fprintf(&sb, "時間帯: %s ～ %s\n\n",
		localStart.Format("2006-01-02 15:04"),
		localStart.Add(time.Hour).Format("15:04"))

	if len(prevDigests) > 0 {
		sb.WriteString("## 前の記録（被らないように）\n")
		for _, d := range prevDigests {
			fmt.Fprintf(&sb, "- [%s] %s\n", d.PeriodStart.Format("2006-01-02T15:00"), d.Content)
		}
		sb.WriteString("\n")
	}

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

	if len(memos) > 0 {
		sb.WriteString("## メモ\n")
		for _, m := range memos {
			ts := jtime.In(m.CreatedAt).Format("15:04")
			fmt.Fprintf(&sb, "- [%s] %s\n", ts, textutil.TruncateRunes(m.Content, 200))
		}
		sb.WriteString("\n")
	}

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
