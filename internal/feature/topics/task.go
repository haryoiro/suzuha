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

	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/user"
)

const (
	// boredomRate is the boredom increase per hour since last interaction.
	// 100 / 8.0 ≈ 12.5 hours to reach maximum boredom.
	boredomRate = 8.0

	// boredomMax is the ceiling for the boredom score.
	boredomMax = 100.0

	// postThresholdMin is the minimum boredom level required to consider posting.
	// At boredomRate=8, this means ~20 minutes of silence before any post is possible.
	postThresholdMin = 3.0

	// postProbabilityMax is the posting probability at maximum boredom.
	postProbabilityMax = 0.85

	// mentionBoredomMin is the minimum boredom level to consider mentioning someone.
	mentionBoredomMin = 50.0

	// mentionProbabilityMax is the mention probability at maximum boredom.
	mentionProbabilityMax = 0.40
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
	return jtime.Now()
}

// mutteringConfig holds task-specific configuration from config.yaml.
type mutteringConfig struct {
	ChannelID   string `json:"channel_id"`
	SkipBoredom bool   `json:"skip_boredom"`
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var mc mutteringConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &mc)
	}
	if cc.Bus == nil {
		cc.Logger.Warn("topics: no event bus available, skipping")
		return nil
	}

	// --- Boredom-based posting decision ---
	now := t.now()
	var lastInteraction time.Time
	if cc.ChannelActivity != nil {
		lastInteraction, _, _ = cc.ChannelActivity.LastInteractionGlobal(ctx)
	}
	boredom := calcBoredom(now, lastInteraction)

	cc.Logger.Info("topics: boredom check",
		"boredom", fmt.Sprintf("%.1f", boredom),
		"last_interaction", lastInteraction,
		"channel_id", mc.ChannelID)

	if !mc.SkipBoredom && !shouldPost(boredom) {
		cc.Logger.Info("topics: skipping (low boredom or probability miss)",
			"boredom", fmt.Sprintf("%.1f", boredom))
		return nil
	}

	// Choose channel: high boredom → wander to any active channel, low → home only.
	if mc.ChannelID == "" {
		if boredom >= 50 {
			mc.ChannelID = findRandomActiveChannel(ctx, cc.DB)
		}
		if mc.ChannelID == "" {
			mc.ChannelID = findHomeChannel(ctx, cc.DB)
		}
	}
	if mc.ChannelID == "" {
		cc.Logger.Warn("topics: no channel found, skipping")
		return nil
	}

	// Build context for self-prompt.
	localNow := jtime.In(now)
	timeHint := buildTimeHint(localNow)

	recentMemories := fetchRecentContext(ctx, cc, 8)
	pastMutterings := fetchPastMutterings(ctx, cc, 8)

	var mentionables []user.MentionableUser
	if cc.Users != nil {
		mentionables, _ = cc.Users.ListMentionable(ctx)
	}
	mentionTarget := selectMentionTarget(boredom, mentionables)

	// Publish self-prompt event to agent pipeline.
	prompt := buildSelfPrompt(localNow, timeHint, boredom, recentMemories, pastMutterings, mentionTarget)
	evt := event.NewSelfPromptEvent(mc.ChannelID, prompt)
	cc.Bus.Publish(evt)

	cc.Logger.Info("topics: published self-prompt event",
		"channel_id", mc.ChannelID,
		"boredom", fmt.Sprintf("%.1f", boredom))

	// Record post time to prevent rapid re-triggering.
	t.mu.Lock()
	t.lastPostedAt = now
	t.mu.Unlock()
	t.saveState(ctx, cc)

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

// fetchPastMutterings retrieves recent bot messages from conversation_logs
// so that self-prompts can avoid repeating the same topics.
func fetchPastMutterings(ctx context.Context, cc *scheduler.CronContext, limit int) []memory.Memory {
	if cc.DB == nil {
		return nil
	}
	rows, err := cc.DB.QueryContext(ctx,
		`SELECT content, timestamp FROM conversation_logs
		 WHERE role = 'assistant' AND content != ''
		 ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil {
		cc.Logger.Debug("topics: fetch past mutterings", "error", err)
		return nil
	}
	defer rows.Close()

	var results []memory.Memory
	for rows.Next() {
		var content string
		var ts time.Time
		if err := rows.Scan(&content, &ts); err != nil {
			continue
		}
		results = append(results, memory.Memory{Content: content, CreatedAt: ts})
	}
	return results
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

// buildSelfPrompt builds the content for a self-prompt event.
func buildSelfPrompt(
	now time.Time,
	timeHint string,
	boredom float64,
	recentMemories []memory.Memory,
	pastMutterings []memory.Memory,
	mentionTarget *user.MentionableUser,
) string {
	var sb strings.Builder

	sb.WriteString("[ふと意識が浮かぶ]\n\n")
	fmt.Fprintf(&sb, "今: %s %s\n", now.Format("2006-01-02"), now.Format("15:04"))
	fmt.Fprintf(&sb, "退屈レベル: %.0f / 100（%s）\n\n", boredom, boredomLabel(boredom))

	// 退屈度に応じた行動指針
	switch {
	case boredom >= 70:
		sb.WriteString("かなり暇。誰かに話しかけたい気分。\n")
		if mentionTarget != nil {
			fmt.Fprintf(&sb, "%sさん (Discord: <@%s>) に声をかけてみてもいい。\n", mentionTarget.DisplayName, mentionTarget.DiscordUserID)
		}
		sb.WriteString("気になることを調べたり、音楽を変えたり、積極的に動いていい。\n\n")
	case boredom >= 40:
		sb.WriteString("そこそこ暇。ひとりごとを言ったり、気になることを調べたりしてもいい。\n")
		if mentionTarget != nil {
			fmt.Fprintf(&sb, "%sさん (Discord: <@%s>) がいるけど、用がなければ話しかけなくていい。\n", mentionTarget.DisplayName, mentionTarget.DiscordUserID)
		}
		sb.WriteString("\n")
	default:
		sb.WriteString("まだそんなに暇じゃない。誰かに話しかける必要はない。\n")
		sb.WriteString("ステータスを変えたり、気になることを考えたり調べたり、何かを作ったりしてもいい。\n\n")
	}

	if len(recentMemories) > 0 {
		sb.WriteString("最近の記憶の断片:\n")
		for _, m := range recentMemories {
			fmt.Fprintf(&sb, "- %s\n", textutil.TruncateRunes(m.Content, 80))
		}
		sb.WriteString("\n")
	}

	if len(pastMutterings) > 0 {
		sb.WriteString("最近の自分の発言（同じ話題を繰り返さないこと）:\n")
		for _, m := range pastMutterings {
			fmt.Fprintf(&sb, "- %s\n", textutil.TruncateRunes(m.Content, 80))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("同じようなことの繰り返しにならないように\n\n")
	sb.WriteString("- 短く自然に 綺麗にまとめない\n")
	sb.WriteString("- 絵文字・顔文字は使わない\n")
	sb.WriteString("- 時報禁止（「0時。」「もう3時か」等、時刻を報告するだけの発言はしない）\n")
	sb.WriteString("- ツールを使ったことを報告しない（「調べてみた」「検索した」「記録した」等のメタ発言禁止）\n")
	hour := now.Hour()
	if hour >= 22 || hour < 6 {
		sb.WriteString("- 基本的に skip_response でスキップ。本当に言いたいことがあるときだけ発言\n")
	} else {
		sb.WriteString("- 特に何もなければ skip_response ツールを呼んでスキップしてよい\n")
	}

	return sb.String()
}

// selectMentionTarget probabilistically picks a user to mention based on boredom.
// Selection is uniformly random among mentionable users.
func selectMentionTarget(boredom float64, users []user.MentionableUser) *user.MentionableUser {
	if len(users) == 0 {
		return nil
	}
	threshold := mentionBoredomMin
	if boredom < threshold {
		return nil
	}
	prob := (boredom - threshold) / (boredomMax - threshold) * mentionProbabilityMax
	if rand.Float64() >= prob {
		return nil
	}
	return &users[rand.IntN(len(users))]
}

// findHomeChannel looks up the home channel from channel_settings.
// Only returns channels with active mode (not disabled/listen).
// findRandomActiveChannel picks a random active channel from any server.
func findRandomActiveChannel(ctx context.Context, db *sql.DB) string {
	if db == nil {
		return ""
	}
	rows, err := db.QueryContext(ctx,
		`SELECT channel_id FROM channel_settings WHERE mode = 'active' OR mode = ''`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var ch string
		if rows.Scan(&ch) == nil {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		return ""
	}
	return channels[rand.IntN(len(channels))]
}

func findHomeChannel(ctx context.Context, db *sql.DB) string {
	var channelID string
	err := db.QueryRowContext(ctx,
		`SELECT channel_id FROM channel_settings WHERE home = true AND (mode = 'active' OR mode = '') LIMIT 1`,
	).Scan(&channelID)
	if err != nil {
		return ""
	}
	return channelID
}

