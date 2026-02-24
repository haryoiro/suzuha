package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// maxBackoff is the maximum number of consecutive cron executions to skip
// when no user response is detected. With a 1-hour cron, 5 means at most
// 6 hours between topic posts.
const maxBackoff = 5

// topicAction represents the action type for topic generation.
type topicAction string

const (
	actionNew        topicAction = "NEW"
	actionReply      topicAction = "REPLY"
	actionSupplement topicAction = "SUPPLEMENT"
)

// TopicsTask implements scheduler.CronTask for periodic topic posting with opt-in mentions.
type TopicsTask struct {
	mu                    sync.Mutex
	consecutiveNoResponse int
	skipCounter           int
	lastPostedAt          time.Time
	// nowFunc is used for testing; defaults to time.Now.
	nowFunc func() time.Time
}

var _ scheduler.CronTask = (*TopicsTask)(nil)

func (t *TopicsTask) Name() string        { return "topics" }
func (t *TopicsTask) Description() string { return "定期的に話題を提供・メンション" }

func (t *TopicsTask) Setup(_ context.Context, _ *scheduler.CronContext) error {
	return nil
}

func (t *TopicsTask) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return time.Now()
}

// topicsConfig holds task-specific configuration from config.yaml.
type topicsConfig struct {
	ChannelID string   `json:"channel_id"`
	Topics    []string `json:"topics"`
	PromptDir string   `json:"prompt_dir"`
	Timezone  string   `json:"timezone"` // IANA timezone for time-aware prompts.
}

// optInUser holds a user who opted in to mentions along with their Discord ID.
type optInUser struct {
	UserID         string
	DisplayName    string
	PlatformUserID string // Discord user ID for <@ID> mentions
}

// previousTopic holds a past topic post with its metadata.
type previousTopic struct {
	Memory    memory.Memory
	Topic     string
	Responded bool
	MessageID string
}

// actionDecision holds the result of LLM action selection.
type actionDecision struct {
	Action      topicAction
	ReplyTarget *previousTopic // non-nil for REPLY and SUPPLEMENT
}

func (t *TopicsTask) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var tc topicsConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &tc)
	}
	if tc.ChannelID == "" {
		cc.Logger.Warn("topics: no channel_id configured, skipping")
		return nil
	}
	if len(tc.Topics) == 0 {
		cc.Logger.Warn("topics: no topics configured, skipping")
		return nil
	}

	// --- Backoff logic: skip if the previous topic got no response ---
	t.mu.Lock()
	if t.skipCounter > 0 {
		t.skipCounter--
		cc.Logger.Info("topics: backoff skip",
			"remaining_skips", t.skipCounter,
			"consecutive_no_response", t.consecutiveNoResponse)
		t.mu.Unlock()
		return nil
	}

	// Check whether anyone responded since the last topic post.
	hadResponse := false
	if !t.lastPostedAt.IsZero() && cc.DB != nil {
		hadResponse = hasChannelActivity(ctx, cc.DB, tc.ChannelID, t.lastPostedAt)
		if hadResponse {
			t.consecutiveNoResponse = 0
			cc.Logger.Info("topics: response detected, resetting backoff")
		} else {
			t.consecutiveNoResponse++
			cc.Logger.Info("topics: no response detected",
				"consecutive_no_response", t.consecutiveNoResponse)
		}
	}

	// Set backoff for subsequent executions.
	backoff := t.consecutiveNoResponse
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	t.skipCounter = backoff
	t.mu.Unlock()

	// 1. Load system prompt from IDENTITY.md / SOUL.md.
	systemPrompt := loadPromptFiles(tc.PromptDir)

	// 2. Get opt-in users.
	users, err := getOptInUsers(ctx, cc.DB)
	if err != nil {
		cc.Logger.Error("topics: get opt-in users", "error", err)
		users = nil
	}

	// 3. Collect user memories for context.
	userContexts := buildUserContexts(ctx, cc, users)

	// 4. Fetch recent context and past topics.
	recentMemories := fetchRecentContext(ctx, cc, 10)
	previousTopics := fetchPreviousTopics(ctx, cc, 10)

	// 5. Mark previous topic's response status.
	if len(previousTopics) > 0 && !t.lastPostedAt.IsZero() {
		markTopicResponse(ctx, cc, &previousTopics[0], hadResponse)
	}

	// 6. Resolve timezone and build time hint.
	loc := resolveTimezone(tc.Timezone)
	if tc.Timezone == "" && cc.Timezone != nil {
		loc = cc.Timezone
	}
	now := t.now().In(loc)
	timeHint := buildTimeHint(now)

	// 7. Decide action: NEW, REPLY, or SUPPLEMENT.
	decision := decideAction(ctx, cc, systemPrompt, previousTopics, recentMemories, timeHint, now)

	// 8. Pick a topic seed for NEW action.
	topicSeed := tc.Topics[rand.IntN(len(tc.Topics))]

	// 9. Generate message via LLM.
	message, err := generateTopicMessageV2(ctx, cc, generateTopicParams{
		SystemPrompt:    systemPrompt,
		Action:          decision.Action,
		TopicSeed:       topicSeed,
		ReplyTarget:     decision.ReplyTarget,
		UserContexts:    userContexts,
		RecentMemories:  recentMemories,
		PreviousTopics:  previousTopics,
		TimeHint:        timeHint,
		Now:             now,
	})
	if err != nil {
		cc.Logger.Error("topics: generate message", "error", err)
		return nil
	}

	// 10. Send notification.
	var messageID string
	replyTo := ""
	if decision.ReplyTarget != nil {
		replyTo = decision.ReplyTarget.MessageID
	}

	if cc.ReplyNotifier != nil && replyTo != "" {
		messageID, err = cc.ReplyNotifier.Reply(ctx, tc.ChannelID, message, replyTo, "topics")
	} else if cc.ReplyNotifier != nil {
		messageID, err = cc.ReplyNotifier.Notify(ctx, tc.ChannelID, message, "topics")
	} else {
		err = cc.Notifier(ctx, tc.ChannelID, message, "topics")
	}
	if err != nil {
		cc.Logger.Error("topics: notify", "error", err)
		return fmt.Errorf("topics: notify: %w", err)
	}

	// 11. Save to memory for history.
	mem := &memory.Memory{
		Type:    memory.MemoryTypeWorld,
		Content: fmt.Sprintf("話題提供: %s\n%s", topicSeed, message),
		Metadata: map[string]any{
			"source":     "topics",
			"topic":      topicSeed,
			"action":     string(decision.Action),
			"channel_id": tc.ChannelID,
		},
	}
	if messageID != "" {
		mem.Metadata["message_id"] = messageID
	}
	if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
		cc.Logger.Error("topics: save memory", "error", saveErr)
	}

	// 12. Record post time for next backoff check.
	t.mu.Lock()
	t.lastPostedAt = t.now()
	t.mu.Unlock()

	cc.Logger.Info("topics: posted",
		"topic", topicSeed,
		"action", string(decision.Action),
		"users", len(users),
		"message_id", messageID,
		"reply_to", replyTo,
		"next_skip", backoff)
	return nil
}

// hasChannelActivity checks if any user message was sent in the channel after since.
func hasChannelActivity(ctx context.Context, db *sql.DB, channelID string, since time.Time) bool {
	var exists int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM channel_activity
		 WHERE channel_id = ? AND last_user_message_at > ?`,
		channelID, since,
	).Scan(&exists)
	return err == nil && exists == 1
}

// loadPromptFiles reads IDENTITY.md and SOUL.md from the given directory.
func loadPromptFiles(dir string) string {
	if dir == "" {
		return ""
	}
	var parts []string
	for _, name := range []string{"IDENTITY.md", "SOUL.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}
	return strings.Join(parts, "\n\n")
}

// getOptInUsers queries users who have opted in to mentions.
func getOptInUsers(ctx context.Context, db *sql.DB) ([]optInUser, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.display_name, pl.platform_user_id
		FROM users u
		JOIN platform_links pl ON pl.user_id = u.id AND pl.platform = 'discord'
		WHERE json_extract(u.metadata, '$.mention_opt_in') = 1
		  AND u.is_bot = 0
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []optInUser
	for rows.Next() {
		var u optInUser
		if err := rows.Scan(&u.UserID, &u.DisplayName, &u.PlatformUserID); err != nil {
			continue
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// userContext holds a user and their known memories.
type userContext struct {
	User     optInUser
	Memories []memory.Memory
}

// buildUserContexts retrieves known memories about each opt-in user.
func buildUserContexts(ctx context.Context, cc *scheduler.CronContext, users []optInUser) []userContext {
	var contexts []userContext
	for _, u := range users {
		query := u.DisplayName
		if query == "" {
			query = u.UserID
		}
		mems, err := cc.Memory.SearchByType(ctx, query, memory.MemoryTypeUser, 5)
		if err != nil {
			cc.Logger.Debug("topics: search user memories", "user", u.UserID, "error", err)
		}
		contexts = append(contexts, userContext{User: u, Memories: mems})
	}
	return contexts
}

// fetchRecentContext retrieves recent memories for conversational context.
func fetchRecentContext(ctx context.Context, cc *scheduler.CronContext, limit int) []memory.Memory {
	since := time.Now().Add(-48 * time.Hour)
	mems, err := cc.Memory.SearchRecent(ctx, "話題 会話", limit, since)
	if err != nil {
		cc.Logger.Debug("topics: search recent context", "error", err)
		return nil
	}
	return mems
}

// fetchPreviousTopics retrieves past topic posts from memory.
func fetchPreviousTopics(ctx context.Context, cc *scheduler.CronContext, limit int) []previousTopic {
	mems, err := cc.Memory.SearchByType(ctx, "話題提供", memory.MemoryTypeWorld, limit)
	if err != nil {
		cc.Logger.Debug("topics: search previous topics", "error", err)
		return nil
	}

	var topics []previousTopic
	for _, m := range mems {
		if m.Metadata == nil {
			continue
		}
		source, _ := m.Metadata["source"].(string)
		if source != "topics" {
			continue
		}
		topic, _ := m.Metadata["topic"].(string)
		responded, _ := m.Metadata["responded"].(bool)
		msgID, _ := m.Metadata["message_id"].(string)
		topics = append(topics, previousTopic{
			Memory:    m,
			Topic:     topic,
			Responded: responded,
			MessageID: msgID,
		})
	}
	return topics
}

// markTopicResponse updates the responded flag on a previous topic memory.
func markTopicResponse(ctx context.Context, cc *scheduler.CronContext, pt *previousTopic, responded bool) {
	pt.Responded = responded
	if pt.Memory.Metadata == nil {
		pt.Memory.Metadata = make(map[string]any)
	}
	pt.Memory.Metadata["responded"] = responded

	// Update via AdminStore if available.
	if as, ok := cc.Memory.(memory.AdminStore); ok {
		if err := as.Update(ctx, &pt.Memory); err != nil {
			cc.Logger.Debug("topics: update topic response status", "error", err)
		}
	}
}

// resolveTimezone parses a timezone string, falling back to UTC.
func resolveTimezone(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// buildTimeHint returns a time-of-day hint for the LLM prompt.
func buildTimeHint(now time.Time) string {
	hour := now.Hour()
	switch {
	case hour >= 6 && hour < 11:
		return "朝の時間帯です。軽い挨拶や一日の始まりに合う話題が自然です。"
	case hour >= 11 && hour < 14:
		return "お昼の時間帯です。"
	case hour >= 14 && hour < 18:
		return "午後の時間帯です。"
	case hour >= 18 && hour < 22:
		return "夜の時間帯です。リラックスした話題が合います。"
	default:
		return "深夜の時間帯です。"
	}
}

// decideAction uses LLM to decide the best action (NEW, REPLY, SUPPLEMENT).
func decideAction(ctx context.Context, cc *scheduler.CronContext, systemPrompt string, previousTopics []previousTopic, recentMemories []memory.Memory, timeHint string, now time.Time) actionDecision {
	// If no previous topics with message_id, default to NEW.
	var replyableTopics []previousTopic
	for _, pt := range previousTopics {
		if pt.MessageID != "" {
			replyableTopics = append(replyableTopics, pt)
		}
	}
	if len(replyableTopics) == 0 {
		return actionDecision{Action: actionNew}
	}

	// Build decision prompt.
	var sb strings.Builder
	sb.WriteString("以下の状況で、チャンネルに対してどのアクションが最も自然ですか？\n\n")

	sb.WriteString("## 前回の話題\n")
	for i, pt := range replyableTopics {
		status := "反応なし"
		if pt.Responded {
			status = "反応あり"
		}
		fmt.Fprintf(&sb, "[%d] 「%s」（%s）\n", i, truncateStr(pt.Memory.Content, 100), status)
	}
	sb.WriteString("\n")

	if len(recentMemories) > 0 {
		sb.WriteString("## 最近の会話の流れ\n")
		for _, m := range recentMemories {
			fmt.Fprintf(&sb, "- %s\n", truncateStr(m.Content, 80))
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "## 現在の時刻\n%s — %s\n\n", now.Format("15:04"), timeHint)

	sb.WriteString("以下から1つ選んでください:\n")
	sb.WriteString("- NEW: 新しい話題を投稿する\n")
	sb.WriteString("- REPLY: 前回の話題にリプライして会話を続ける\n")
	sb.WriteString("- SUPPLEMENT: 過去の話題に「ちなみに〜」「補足すると〜」等で追加情報を加える\n\n")
	sb.WriteString("REPLY/SUPPLEMENTの場合は対象のインデックスも付けてください（例: REPLY:0, SUPPLEMENT:2）。\n")
	sb.WriteString("1行だけで答えてください。\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		cc.Logger.Debug("topics: action decision failed, defaulting to NEW", "error", err)
		return actionDecision{Action: actionNew}
	}

	return parseActionDecision(resp.Text, replyableTopics)
}

// parseActionDecision parses the LLM response into an actionDecision.
func parseActionDecision(text string, replyableTopics []previousTopic) actionDecision {
	text = strings.TrimSpace(text)
	upper := strings.ToUpper(text)

	// Parse "ACTION:INDEX" format.
	parts := strings.SplitN(upper, ":", 2)
	action := topicAction(strings.TrimSpace(parts[0]))

	var target *previousTopic
	if len(parts) == 2 {
		idx := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &idx); err == nil {
			if idx >= 0 && idx < len(replyableTopics) {
				target = &replyableTopics[idx]
			}
		}
	}

	switch action {
	case actionReply:
		if target == nil && len(replyableTopics) > 0 {
			target = &replyableTopics[0]
		}
		return actionDecision{Action: actionReply, ReplyTarget: target}
	case actionSupplement:
		if target == nil && len(replyableTopics) > 0 {
			target = &replyableTopics[0]
		}
		return actionDecision{Action: actionSupplement, ReplyTarget: target}
	default:
		return actionDecision{Action: actionNew}
	}
}

// generateTopicParams holds parameters for topic message generation.
type generateTopicParams struct {
	SystemPrompt   string
	Action         topicAction
	TopicSeed      string
	ReplyTarget    *previousTopic
	UserContexts   []userContext
	RecentMemories []memory.Memory
	PreviousTopics []previousTopic
	TimeHint       string
	Now            time.Time
}

// generateTopicMessageV2 generates a context-aware topic message via LLM.
func generateTopicMessageV2(ctx context.Context, cc *scheduler.CronContext, p generateTopicParams) (string, error) {
	var sb strings.Builder

	// Action-specific instruction.
	switch p.Action {
	case actionReply:
		if p.ReplyTarget != nil {
			fmt.Fprintf(&sb, "前回の話題「%s」にリプライして会話を続けてください。\n\n",
				truncateStr(p.ReplyTarget.Memory.Content, 150))
		}
	case actionSupplement:
		if p.ReplyTarget != nil {
			fmt.Fprintf(&sb, "以前の話題「%s」に補足情報・関連ネタを加えてください。「ちなみに」「そういえば」等で自然に。\n\n",
				truncateStr(p.ReplyTarget.Memory.Content, 150))
		}
	default:
		fmt.Fprintf(&sb, "以下のトピックについて、チャンネルに投稿する新しい話題を1つ生成してください。\n\n")
		fmt.Fprintf(&sb, "## トピック\n%s\n\n", p.TopicSeed)
	}

	// Time context.
	fmt.Fprintf(&sb, "## 現在の時刻\n%s — %s\n\n", p.Now.Format("15:04"), p.TimeHint)

	// Recent conversation context.
	if len(p.RecentMemories) > 0 {
		sb.WriteString("## 最近のチャンネルの話題 (参考)\n")
		for _, m := range p.RecentMemories {
			fmt.Fprintf(&sb, "- %s\n", truncateStr(m.Content, 100))
		}
		sb.WriteString("\n")
	}

	// Past topics with response status.
	if len(p.PreviousTopics) > 0 {
		sb.WriteString("## 過去に提供した話題\n")
		for _, pt := range p.PreviousTopics {
			if pt.Responded {
				fmt.Fprintf(&sb, "- ✅ [反応あり] %s\n", truncateStr(pt.Memory.Content, 80))
			} else {
				fmt.Fprintf(&sb, "- ❌ [反応なし] %s\n", truncateStr(pt.Memory.Content, 80))
			}
		}
		sb.WriteString("\n")
	}

	// Mentionable users.
	if len(p.UserContexts) > 0 {
		sb.WriteString("## メンション可能なユーザー\n")
		sb.WriteString("以下のユーザーにはメンション（Discord形式: <@DiscordユーザーID>）してよいです。\n\n")
		for _, uc := range p.UserContexts {
			name := uc.User.DisplayName
			if name == "" {
				name = uc.User.PlatformUserID
			}
			fmt.Fprintf(&sb, "- %s (<@%s>)\n", name, uc.User.PlatformUserID)
			if len(uc.Memories) > 0 {
				sb.WriteString("  既知情報:\n")
				for _, m := range uc.Memories {
					fmt.Fprintf(&sb, "  - %s\n", truncateStr(m.Content, 100))
				}
			} else {
				sb.WriteString("  既知情報: まだあまり知らない\n")
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 方針\n")
	sb.WriteString("- 最近の会話の流れに自然に繋がるような話題にする\n")
	sb.WriteString("- 時間帯に合ったトーンで\n")
	sb.WriteString("- 反応がなかった話題は避けるか別アプローチで\n")
	sb.WriteString("- 過去に提供した話題とは異なる切り口で\n")
	sb.WriteString("- 独り言や呟き程度の気軽なトーンで。質問形式は避ける（返事を強制しない）\n")
	sb.WriteString("- 200文字以内で自然に\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if p.SystemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: p.SystemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	return resp.Text, nil
}

// truncateStr shortens a string to maxRunes runes.
func truncateStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
