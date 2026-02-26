package topics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

const (
	// boredomRate is the boredom increase per hour since last interaction.
	// 100 / 8.0 ≈ 12.5 hours to reach maximum boredom.
	boredomRate = 8.0

	// boredomMax is the ceiling for the boredom score.
	boredomMax = 100.0

	// postThresholdMin is the minimum boredom level required to consider posting.
	// At boredomRate=8, this means ~2.5 hours of silence before any post is possible.
	postThresholdMin = 20.0

	// postProbabilityMax is the posting probability at maximum boredom.
	postProbabilityMax = 0.85
)

// persistedState is the JSON-serializable state saved to task_state table.
type persistedState struct {
	LastPostedAt time.Time `json:"last_posted_at"`
}

// Task implements scheduler.CronTask for periodic muttering.
type Task struct {
	mu           sync.Mutex
	lastPostedAt time.Time
	// nowFunc is used for testing; defaults to time.Now.
	nowFunc func() time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "topics" }
func (t *Task) Description() string { return "独り言をつぶやく" }

func (t *Task) Setup(ctx context.Context, cc *scheduler.CronContext) error {
	if cc.DB == nil {
		return nil
	}
	var s persistedState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("topics: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.lastPostedAt = s.LastPostedAt
	t.mu.Unlock()
	cc.Logger.Info("topics: restored state",
		"last_posted_at", s.LastPostedAt)
	return nil
}

func (t *Task) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return time.Now()
}

// mutteringConfig holds task-specific configuration from config.yaml.
type mutteringConfig struct {
	ChannelID string `json:"channel_id"`
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var mc mutteringConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &mc)
	}
	if mc.ChannelID == "" {
		cc.Logger.Warn("topics: no channel_id configured, skipping")
		return nil
	}

	// --- Boredom-based posting decision ---
	now := t.now()
	lastInteraction := getLastInteraction(ctx, cc.DB, mc.ChannelID)
	boredom := calcBoredom(now, lastInteraction)

	cc.Logger.Info("topics: boredom check",
		"boredom", fmt.Sprintf("%.1f", boredom),
		"last_interaction", lastInteraction,
		"channel_id", mc.ChannelID)

	if !shouldPost(boredom) {
		cc.Logger.Info("topics: skipping (low boredom or probability miss)",
			"boredom", fmt.Sprintf("%.1f", boredom))
		return nil
	}

	// 1. System prompt.
	systemPrompt := cc.SystemPrompt

	// 2. Resolve timezone and build time hint.
	loc := cc.Timezone
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	timeHint := buildTimeHint(localNow)

	// 3. Fetch recent context and past mutterings for diversity.
	recentMemories := fetchRecentContext(ctx, cc, 8)
	pastMutterings := fetchPastMutterings(ctx, cc, 8)

	// 4. Generate muttering via LLM (boredom-aware).
	message, err := generateMuttering(ctx, cc, systemPrompt, localNow, timeHint, boredom, recentMemories, pastMutterings)
	if err != nil {
		cc.Logger.Error("topics: generate muttering", "error", err)
		return nil
	}

	// 5. Send.
	result, err := cc.Notifier.Send(ctx, mc.ChannelID, message, "topics")
	if err != nil {
		cc.Logger.Error("topics: notify", "error", err)
		return fmt.Errorf("topics: notify: %w", err)
	}

	// 6. Save to memory for history / diversity.
	mem := &memory.Memory{
		Type:    memory.MemoryTypeWorld,
		Content: fmt.Sprintf("独り言: %s", message),
		Metadata: map[string]any{
			"source":     "topics",
			"channel_id": mc.ChannelID,
			"boredom":    boredom,
		},
	}
	if result.MessageID != "" {
		mem.Metadata["message_id"] = result.MessageID
	}
	if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
		cc.Logger.Error("topics: save memory", "error", saveErr)
	}

	// 7. Record post time.
	t.mu.Lock()
	t.lastPostedAt = now
	t.mu.Unlock()

	t.saveState(ctx, cc)

	cc.Logger.Info("topics: posted",
		"message_id", result.MessageID,
		"boredom", fmt.Sprintf("%.1f", boredom))
	return nil
}

// saveState persists the current state to SQLite.
func (t *Task) saveState(ctx context.Context, cc *scheduler.CronContext) {
	if cc.DB == nil {
		return
	}
	t.mu.Lock()
	s := persistedState{
		LastPostedAt: t.lastPostedAt,
	}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("topics: save state", "error", err)
	}
}

// getLastInteraction queries the last user message time for the channel.
func getLastInteraction(ctx context.Context, db *sql.DB, channelID string) time.Time {
	if db == nil {
		return time.Time{}
	}
	var lastMsg time.Time
	err := db.QueryRowContext(ctx,
		`SELECT last_user_message_at FROM channel_activity WHERE channel_id = ?`,
		channelID,
	).Scan(&lastMsg)
	if err != nil {
		return time.Time{} // no activity → maximum boredom
	}
	return lastMsg
}

// calcBoredom computes a boredom score (0–100) from time since last interaction.
func calcBoredom(now time.Time, lastInteraction time.Time) float64 {
	if lastInteraction.IsZero() {
		return boredomMax
	}
	hours := now.Sub(lastInteraction).Hours()
	if hours < 0 {
		return 0
	}
	b := hours * boredomRate
	if b > boredomMax {
		return boredomMax
	}
	return b
}

// shouldPost decides whether to post based on boredom level (probabilistic).
func shouldPost(boredom float64) bool {
	if boredom < postThresholdMin {
		return false
	}
	// Linear interpolation from 0 at threshold to postProbabilityMax at 100.
	prob := (boredom - postThresholdMin) / (boredomMax - postThresholdMin) * postProbabilityMax
	return rand.Float64() < prob
}

// boredomLabel returns a human-readable label for the boredom level.
func boredomLabel(boredom float64) string {
	switch {
	case boredom >= 80:
		return "かなり暇。誰かと話したい"
	case boredom >= 50:
		return "そこそこ暇"
	case boredom >= 30:
		return "ちょっと暇かも"
	default:
		return "まだ暇じゃない"
	}
}

// fetchRecentContext retrieves recent memories for conversational context.
func fetchRecentContext(ctx context.Context, cc *scheduler.CronContext, limit int) []memory.Memory {
	since := time.Now().Add(-48 * time.Hour)
	mems, err := cc.Memory.SearchRecent(ctx, "会話", limit, since)
	if err != nil {
		cc.Logger.Debug("topics: search recent context", "error", err)
		return nil
	}
	return mems
}

// fetchPastMutterings retrieves past muttering posts from memory.
func fetchPastMutterings(ctx context.Context, cc *scheduler.CronContext, limit int) []memory.Memory {
	mems, err := cc.Memory.SearchByType(ctx, "独り言", memory.MemoryTypeWorld, limit)
	if err != nil {
		cc.Logger.Debug("topics: search past mutterings", "error", err)
		return nil
	}
	var filtered []memory.Memory
	for _, m := range mems {
		if m.Metadata == nil {
			continue
		}
		if source, _ := m.Metadata["source"].(string); source == "topics" {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// buildTimeHint returns a time-of-day hint for the LLM prompt.
func buildTimeHint(now time.Time) string {
	hour := now.Hour()
	switch {
	case hour >= 6 && hour < 11:
		return "朝"
	case hour >= 11 && hour < 14:
		return "昼"
	case hour >= 14 && hour < 18:
		return "午後"
	case hour >= 18 && hour < 22:
		return "夜"
	default:
		return "深夜"
	}
}

// generateMuttering generates a short muttering message via LLM.
func generateMuttering(
	ctx context.Context,
	cc *scheduler.CronContext,
	systemPrompt string,
	now time.Time,
	timeHint string,
	boredom float64,
	recentMemories []memory.Memory,
	pastMutterings []memory.Memory,
) (string, error) {
	var sb strings.Builder

	sb.WriteString("独り言をひとつつぶやいて。誰かに話しかけるんじゃなくて、ふと頭に浮かんだことをそのままぽろっと。\n\n")

	fmt.Fprintf(&sb, "今: %s（%s）\n", now.Format("15:04"), timeHint)
	fmt.Fprintf(&sb, "退屈レベル: %.0f / 100（%s）\n\n", boredom, boredomLabel(boredom))

	if len(recentMemories) > 0 {
		sb.WriteString("最近チャンネルであった話題（参考程度に。無視してもいい）:\n")
		for _, m := range recentMemories {
			fmt.Fprintf(&sb, "- %s\n", truncateStr(m.Content, 80))
		}
		sb.WriteString("\n")
	}

	if len(pastMutterings) > 0 {
		sb.WriteString("最近つぶやいたこと（被らないようにだけ気をつけて）:\n")
		for _, m := range pastMutterings {
			fmt.Fprintf(&sb, "- %s\n", truncateStr(m.Content, 80))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("ルール:\n")
	sb.WriteString("- 完全な独り言。誰にも向けてない\n")
	sb.WriteString("- ふと思ったこと、感じたこと、気づいたこと\n")
	sb.WriteString("- 1文。長くても2文。短いほどいい\n")
	sb.WriteString("- 質問しない。返事を期待しない\n")
	sb.WriteString("- メンションしない\n")
	sb.WriteString("- 架空の具体的事柄（作品名、人名、商品名等）を捏造しない\n")
	sb.WriteString("- 感覚的なこと。気分、天気、時間の感覚、ぼんやりした感想、どうでもいい気づき\n")
	sb.WriteString("- つぶやきだけを出力。前置きや説明は不要\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return strings.TrimSpace(resp.Text), nil
}

// truncateStr shortens a string to maxRunes runes.
func truncateStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
