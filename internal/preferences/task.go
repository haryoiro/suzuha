package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// EvalTask is a CronTask that periodically evaluates pending preferences
// and triggers sharing or deepening for confident ones.
type EvalTask struct {
	store *Store
}

func NewEvalTask(store *Store) *EvalTask {
	return &EvalTask{store: store}
}

var _ scheduler.CronTask = (*EvalTask)(nil)

func (t *EvalTask) Name() string        { return "preference_eval" }
func (t *EvalTask) Description() string { return "好みや価値観を吟味・深化する" }

func (t *EvalTask) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

type evalConfig struct {
	ShareMinConfidence float64 `json:"share_min_confidence"`
}

func (t *EvalTask) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var ec evalConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &ec)
	}
	if ec.ShareMinConfidence <= 0 {
		ec.ShareMinConfidence = 0.8
	}

	// --- Phase 1: Evaluate pending preferences ---
	pending, err := t.store.ListPending(ctx, 5)
	if err != nil {
		cc.Logger.Warn("preference_eval: list pending", "error", err)
		return nil
	}

	if len(pending) > 0 {
		cc.Logger.Info("preference_eval: evaluating pending", "count", len(pending))
		t.evaluateBatch(ctx, cc, pending)
	}

	// --- Phase 2: Feed confident liked topics to explore ---
	confident, err := t.store.ListConfident(ctx, 0.7, 10)
	if err != nil {
		cc.Logger.Warn("preference_eval: list confident", "error", err)
		return nil
	}
	for _, p := range confident {
		if p.Stance != StanceLiked {
			continue
		}
		// Save as world memory so explore can pick it up as an interest.
		mem := &memory.Memory{
			Type:    memory.MemoryTypeSelf,
			Content: fmt.Sprintf("自分の好み: %s（%s）— %s [confidence=%.2f]", p.Topic, p.Category, p.Reasoning, p.Confidence),
			Metadata: map[string]any{
				"source":        "preferences",
				"preference_id": p.ID,
			},
		}
		dupID, _ := cc.Memory.IsDuplicate(ctx, mem.Content, memory.MemoryTypeSelf)
		if dupID == "" {
			_ = cc.Memory.Save(ctx, mem)
			cc.Logger.Info("preference_eval: saved preference to memory", "topic", p.Topic)
		}
	}

	// --- Phase 3: Share to home channel ---
	if cc.Bus == nil {
		return nil
	}
	unshared, err := t.store.ListUnshared(ctx, ec.ShareMinConfidence, 1)
	if err != nil || len(unshared) == 0 {
		return nil
	}
	p := unshared[0]

	homeChannel := findHomeChannel(ctx, cc.DB)
	if homeChannel == "" {
		return nil
	}

	prompt := buildSharePrompt(p)
	cc.Bus.Publish(event.NewSelfPromptEvent(homeChannel, prompt))
	_ = t.store.MarkShared(ctx, p.ID)
	cc.Logger.Info("preference_eval: shared preference",
		"topic", p.Topic, "channel", homeChannel, "confidence", p.Confidence)

	// --- Phase 4: DM users who might be interested ---
	t.notifyInterestedUsers(ctx, cc, confident)

	return nil
}

// evaluateBatch asks the LLM to re-evaluate a batch of pending preferences.
func (t *EvalTask) evaluateBatch(ctx context.Context, cc *scheduler.CronContext, prefs []Preference) {
	var sb strings.Builder
	sb.WriteString("以下は私が最近出会ったトピックです。それぞれについて、自分の価値観や過去の経験に照らして吟味してください。\n")
	sb.WriteString("各トピックについてJSON配列で返してください:\n")
	sb.WriteString(`[{"id": <id>, "stance": "liked|disliked|curious|undecided", "confidence": 0.0-1.0, "reasoning": "理由"}]`)
	sb.WriteString("\n\nトピック:\n")
	for _, p := range prefs {
		fmt.Fprintf(&sb, "- id=%d, topic=%q, category=%q, 現在stance=%s, encounters=%d, 前回の理由=%q\n",
			p.ID, p.Topic, p.Category, p.Stance, p.Encounters, p.Reasoning)
	}
	sb.WriteString("\n吟味のルール:\n")
	sb.WriteString("- 1回しか出会ってないものは慎重に（confidenceを低めに）\n")
	sb.WriteString("- 複数回出会っているものは判断を固めていい\n")
	sb.WriteString("- 本当に自分に合うか、なぜ好き/嫌いか深く考える\n")
	sb.WriteString("- 理由は自分の言葉で、短く\n")

	msgs := []providers.Message{
		{Role: "system", Content: cc.SystemPrompt},
		{Role: "user", Content: sb.String()},
	}

	resp, err := cc.LLM.CompleteRaw(ctx, msgs)
	if err != nil {
		cc.Logger.Warn("preference_eval: llm evaluate", "error", err)
		return
	}

	// Parse response.
	text := strings.TrimSpace(resp.Text)
	text = stripCodeFence(text)

	var results []struct {
		ID         int64   `json:"id"`
		Stance     string  `json:"stance"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		cc.Logger.Warn("preference_eval: parse response", "error", err, "text", truncateStr(text, 200))
		return
	}

	for _, r := range results {
		if r.Confidence < 0 {
			r.Confidence = 0
		}
		if r.Confidence > 1.0 {
			r.Confidence = 1.0
		}
		if err := t.store.MarkEvaluated(ctx, r.ID, Stance(r.Stance), r.Confidence, r.Reasoning); err != nil {
			cc.Logger.Warn("preference_eval: update", "id", r.ID, "error", err)
		} else {
			cc.Logger.Info("preference_eval: evaluated",
				"id", r.ID, "stance", r.Stance, "confidence", r.Confidence)
		}
	}
}

// notifyInterestedUsers sends DMs to users who might be interested in a topic.
func (t *EvalTask) notifyInterestedUsers(ctx context.Context, cc *scheduler.CronContext, prefs []Preference) {
	if cc.Users == nil || cc.Bus == nil {
		return
	}

	// Only share high-confidence liked topics that haven't been shared yet.
	for _, p := range prefs {
		if p.Stance != StanceLiked || p.Confidence < 0.85 || p.Shared {
			continue
		}

		// Search user memories for matching interests.
		mems, err := cc.Memory.SearchByType(ctx, p.Topic, memory.MemoryTypeUser, 5)
		if err != nil || len(mems) == 0 {
			continue
		}

		// Find users mentioned in these memories.
		seen := make(map[string]bool)
		for _, m := range mems {
			uid, _ := m.Metadata["user_id"].(string)
			if uid == "" || seen[uid] {
				continue
			}
			seen[uid] = true

			u, err := cc.Users.Resolve(ctx, uid, "", "")
			if err != nil || u.IsBot {
				continue
			}

			// Build DM prompt via self-prompt event.
			prompt := fmt.Sprintf(
				"[内なる思考 -- %sさんに教えたいことがある]\n\n"+
					"最近「%s」（%s）について考えていて、%sさんも興味ありそうだと思った。\n"+
					"理由: %s\n\n"+
					"自然にDMで共有して。押し付けがましくならないように。discord_send_dm ツールを使って。\n"+
					"user_id: %s",
				u.DisplayName, p.Topic, p.Category, u.DisplayName, p.Reasoning, uid)

			homeChannel := findHomeChannel(ctx, cc.DB)
			if homeChannel == "" {
				continue
			}
			cc.Bus.Publish(event.NewSelfPromptEvent(homeChannel, prompt))
			cc.Logger.Info("preference_eval: notifying user about shared interest",
				"user", u.DisplayName, "topic", p.Topic)
		}
	}
}

func buildSharePrompt(p Preference) string {
	var sb strings.Builder
	sb.WriteString("[内なる思考 -- 好きなものについて語りたい]\n\n")
	fmt.Fprintf(&sb, "最近「%s」（%s）が好きだと確信した。\n", p.Topic, p.Category)
	fmt.Fprintf(&sb, "理由: %s\n", p.Reasoning)
	fmt.Fprintf(&sb, "確信度: %.0f%%\n\n", p.Confidence*100)
	sb.WriteString("ルール:\n")
	sb.WriteString("- 自然に、独り言のように語る（1-3文）\n")
	sb.WriteString("- 「好きだと確信した」みたいなメタ発言はしない\n")
	sb.WriteString("- 自分の言葉で、なぜ好きかを伝える\n")
	return sb.String()
}

func findHomeChannel(ctx context.Context, db *sql.DB) string {
	if db == nil {
		return ""
	}
	var channelID string
	err := db.QueryRowContext(ctx,
		`SELECT channel_id FROM channel_settings WHERE home = 1 AND (mode = 'active' OR mode = '') LIMIT 1`,
	).Scan(&channelID)
	if err != nil {
		return ""
	}
	return channelID
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncateStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
