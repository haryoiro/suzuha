package boredom

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	user "github.com/haryoiro/suzuha/internal/domain/user"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	portconv "github.com/haryoiro/suzuha/internal/port/conversation"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	portuser "github.com/haryoiro/suzuha/internal/port/user"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
)

const (
	// boredomRate は最終インタラクションからの 1 時間あたりの退屈度増加。
	// 100 / 8.0 ≈ 12.5 時間で最大値に到達する。
	boredomRate = 8.0

	// boredomMax は退屈度の上限。
	boredomMax = 100.0

	// postThresholdMin は発言を検討する最低退屈度。
	// boredomRate=8 では無言 ~20 分でこの水準に達する。
	postThresholdMin = 3.0

	// postProbabilityMax は退屈度最大時の発言確率。
	postProbabilityMax = 0.85

	// mentionBoredomMin は mention を検討する最低退屈度。
	mentionBoredomMin = 50.0

	// mentionProbabilityMax は退屈度最大時の mention 確率。
	mentionProbabilityMax = 0.40
)

// persistedState は task_state テーブルに保存する JSON 値。
type persistedState struct {
	LastPostedAt time.Time `json:"last_posted_at"`
}

// Task は定期的な独り言を実現する scheduler.CronTask 実装。
type Task struct {
	db       *sql.DB
	memory   portmem.Memory
	users    portuser.Store
	activity portconv.ActivityStore
	bus      *event.Bus
	logger   *slog.Logger

	mu           sync.Mutex
	lastPostedAt time.Time
	nowFunc      func() time.Time // テスト用に time.Now を差し替え可能
}

// NewTask は boredom Task を生成する。
func NewTask(db *sql.DB, mem portmem.Memory, users portuser.Store, activity portconv.ActivityStore, bus *event.Bus, logger *slog.Logger) *Task {
	return &Task{
		db:       db,
		memory:   mem,
		users:    users,
		activity: activity,
		bus:      bus,
		logger:   logger,
	}
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "topics" }
func (t *Task) Description() string { return "独り言をつぶやく" }

// Setup は前回発言時刻を state ストアから復元する。
func (t *Task) Setup(ctx context.Context) error {
	if t.db == nil {
		return nil
	}
	var s persistedState
	if err := scheduler.LoadState(ctx, t.db, t.Name(), &s); err != nil {
		t.logger.Warn("topics: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.lastPostedAt = s.LastPostedAt
	t.mu.Unlock()
	t.logger.Info("topics: restored state", "last_posted_at", s.LastPostedAt)
	return nil
}

func (t *Task) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return jtime.Now()
}

// mutteringConfig は config.yaml から渡される task 固有設定。
type mutteringConfig struct {
	ChannelID   string `json:"channel_id"`
	SkipBoredom bool   `json:"skip_boredom"`
}

// Execute は退屈度を算出し、確率的に self-prompt イベントを発行する。
func (t *Task) Execute(ctx context.Context, cfg json.RawMessage) error {
	var mc mutteringConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &mc); err != nil {
			t.logger.Warn("topics: config parse failed, using defaults", "error", err)
		}
	}
	if t.bus == nil {
		t.logger.Warn("topics: no event bus available, skipping")
		return nil
	}

	now := t.now()
	var lastInteraction time.Time
	if t.activity != nil {
		var actErr error
		lastInteraction, _, actErr = t.activity.LastInteractionGlobal(ctx)
		if actErr != nil {
			t.logger.Warn("topics: last interaction query failed", "error", actErr)
		}
	}
	boredom := calcBoredom(now, lastInteraction)

	t.logger.Info("topics: boredom check",
		"boredom", fmt.Sprintf("%.1f", boredom),
		"last_interaction", lastInteraction,
		"channel_id", mc.ChannelID)

	if !mc.SkipBoredom && !shouldPost(boredom) {
		t.logger.Info("topics: skipping (low boredom or probability miss)",
			"boredom", fmt.Sprintf("%.1f", boredom))
		return nil
	}

	if mc.ChannelID == "" {
		if boredom >= 50 {
			mc.ChannelID = findRandomActiveChannel(ctx, t.db)
		}
		if mc.ChannelID == "" {
			mc.ChannelID = findHomeChannel(ctx, t.db)
		}
	}
	if mc.ChannelID == "" {
		t.logger.Warn("topics: no channel found, skipping")
		return nil
	}

	localNow := jtime.In(now)
	timeHint := buildTimeHint(localNow)

	recentMemories := t.fetchRecentContext(ctx, 8)
	pastMutterings := t.fetchPastMutterings(ctx, 8)

	var mentionables []user.MentionableUser
	if t.users != nil {
		var mentionErr error
		mentionables, mentionErr = t.users.ListMentionable(ctx)
		if mentionErr != nil {
			t.logger.Warn("topics: list mentionable users failed", "error", mentionErr)
		}
	}
	mentionTarget := selectMentionTarget(boredom, mentionables)

	prompt := buildSelfPrompt(localNow, timeHint, boredom, recentMemories, pastMutterings, mentionTarget)
	evt := event.NewSelfPromptEvent(mc.ChannelID, prompt)
	t.bus.Publish(evt)

	t.logger.Info("topics: published self-prompt event",
		"channel_id", mc.ChannelID,
		"boredom", fmt.Sprintf("%.1f", boredom))

	t.mu.Lock()
	t.lastPostedAt = now
	t.mu.Unlock()
	t.saveState(ctx)

	return nil
}

func (t *Task) saveState(ctx context.Context) {
	if t.db == nil {
		return
	}
	t.mu.Lock()
	s := persistedState{LastPostedAt: t.lastPostedAt}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, t.db, t.Name(), &s); err != nil {
		t.logger.Warn("topics: save state", "error", err)
	}
}

// calcBoredom は最終インタラクションからの経過時間から退屈度 (0-100) を算出する。
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

// shouldPost は退屈度に応じて確率的に発言するかを判定する。
func shouldPost(boredom float64) bool {
	if boredom < postThresholdMin {
		return false
	}
	prob := (boredom - postThresholdMin) / (boredomMax - postThresholdMin) * postProbabilityMax
	return rand.Float64() < prob
}

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

// fetchRecentContext は最近の記憶を取得する (会話の文脈用)。
func (t *Task) fetchRecentContext(ctx context.Context, limit int) []memory.Memory {
	since := time.Now().Add(-48 * time.Hour)
	mems, err := t.memory.SearchRecent(ctx, "会話", limit, since)
	if err != nil {
		t.logger.Debug("topics: search recent context", "error", err)
		return nil
	}
	return mems
}

// fetchPastMutterings は最近の bot 発言を取得し、同じ話題の繰り返しを避ける。
func (t *Task) fetchPastMutterings(ctx context.Context, limit int) []memory.Memory {
	if t.db == nil {
		return nil
	}
	rows, err := t.db.QueryContext(ctx,
		`SELECT content, timestamp FROM conversation_logs
		 WHERE role = 'assistant' AND content != ''
		 ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil {
		t.logger.Debug("topics: fetch past mutterings", "error", err)
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

	switch {
	case boredom >= 70:
		sb.WriteString("かなり暇。誰かに話しかけたい気分。\n")
		if mentionTarget != nil {
			fmt.Fprintf(&sb, "%sさん (Discord: <@%s>) に声をかけてみてもいい。\n", mentionTarget.DisplayName, mentionTarget.DiscordUserID)
		}
		sb.WriteString("\n")
	case boredom >= 40:
		sb.WriteString("そこそこ暇。ひとりごとを言ったり、気になることを調べたりしてもいい。\n")
		if mentionTarget != nil {
			fmt.Fprintf(&sb, "%sさん (Discord: <@%s>) がいるけど、用がなければ話しかけなくていい。\n", mentionTarget.DisplayName, mentionTarget.DiscordUserID)
		}
		sb.WriteString("\n")
	default:
		sb.WriteString("まだそんなに暇じゃない。誰かに話しかける必要はない。\n\n")
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
	sb.WriteString("- ツールを使ったことを報告しない（「調べてみた」「検索した」「記録した」等のメタ発言禁止）\n")
	hour := now.Hour()
	if hour >= 22 || hour < 6 {
		sb.WriteString("- 基本的に skip_response でスキップ。本当に言いたいことがあるときだけ発言\n")
	} else {
		sb.WriteString("- 特に何もなければ skip_response ツールを呼んでスキップしてよい\n")
	}

	return sb.String()
}

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
