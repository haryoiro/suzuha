package affinity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Task implements scheduler.CronTask for lightweight affinity evaluation.
// It detects conversation endings (channel inactivity) and runs a quick
// LLM assessment of affinity changes for short conversations that never
// triggered the full Compact cycle.
type Task struct {
	lastEvaluatedAt time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "affinity_eval" }
func (t *Task) Description() string { return "短い会話の好感度を軽量評価" }

type persistedState struct {
	LastEvaluatedAt time.Time `json:"last_evaluated_at"`
}

func (t *Task) Setup(ctx context.Context, cc *scheduler.CronContext) error {
	if cc.DB == nil {
		return nil
	}
	var s persistedState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("affinity_eval: 状態の読み込みに失敗", "error", err)
		return nil
	}
	t.lastEvaluatedAt = s.LastEvaluatedAt
	cc.Logger.Info("affinity_eval: 状態を復元しました", "last_evaluated_at", s.LastEvaluatedAt)
	return nil
}

// evalConfig holds task-specific configuration from config.yaml.
type evalConfig struct {
	// InactivityMinutes is the minimum minutes of channel inactivity before
	// evaluating. Defaults to 15.
	InactivityMinutes int `json:"inactivity_minutes"`
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var ec evalConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &ec)
	}
	if ec.InactivityMinutes <= 0 {
		ec.InactivityMinutes = 15
	}

	if cc.DB == nil {
		return nil
	}

	// 1. Check if any channel has gone inactive recently.
	inactiveThreshold := time.Now().Add(-time.Duration(ec.InactivityMinutes) * time.Minute)
	hasRecentActivity, err := hasActivityBetween(ctx, cc.DB, t.lastEvaluatedAt, inactiveThreshold)
	if err != nil {
		cc.Logger.Debug("affinity_eval: アクティビティの確認に失敗", "error", err)
		return nil
	}
	if !hasRecentActivity {
		cc.Logger.Debug("affinity_eval: 評価対象の最近のアクティビティがありません")
		return nil
	}

	// 2. Load conversation messages from context_snapshot.
	msgs, err := loadContextMessages(cc.DB)
	if err != nil {
		cc.Logger.Debug("affinity_eval: コンテキストの読み込みに失敗", "error", err)
		return nil
	}

	// 3. Filter to user/assistant messages after lastEvaluatedAt.
	var recent []llm.Message
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if !t.lastEvaluatedAt.IsZero() && !m.Timestamp.After(t.lastEvaluatedAt) {
			continue
		}
		recent = append(recent, m)
	}
	if len(recent) < 2 {
		cc.Logger.Debug("affinity_eval: メッセージが少なすぎます", "count", len(recent))
		return nil
	}

	// 4. Run lightweight LLM evaluation.
	deltas, err := evaluateAffinity(ctx, cc, recent)
	if err != nil {
		cc.Logger.Error("affinity_eval: LLM評価に失敗", "error", err)
		return nil
	}

	// 5. Apply deltas.
	for _, d := range deltas {
		if err := applyDelta(ctx, cc.Users, d); err != nil {
			cc.Logger.Warn("affinity_eval: デルタの適用に失敗", "error", err, "user_id", d.platformUserID)
		} else {
			cc.Logger.Info("affinity_eval: 適用しました",
				"user_id", d.platformUserID, "delta", d.delta, "reason", d.reason)
		}
	}

	// 6. Recalculate effective affinity values (time-decay + soft cap).
	if cc.Users != nil {
		if err := cc.Users.RecalculateEffective(ctx); err != nil {
			cc.Logger.Warn("affinity_eval: 実効値の再計算に失敗", "error", err)
		}
	}

	// 7. Update state.
	now := time.Now()
	t.lastEvaluatedAt = now
	if saveErr := scheduler.SaveState(ctx, cc.DB, t.Name(), &persistedState{LastEvaluatedAt: now}); saveErr != nil {
		cc.Logger.Warn("affinity_eval: 状態の保存に失敗", "error", saveErr)
	}

	return nil
}

// hasActivityBetween checks if any channel had its last user message between after and before.
// This means: activity happened after our last eval, but the channel has been quiet for long enough.
func hasActivityBetween(ctx context.Context, db *sql.DB, after, before time.Time) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM channel_activity WHERE last_user_message_at > ? AND last_user_message_at < ?`
	err := db.QueryRowContext(ctx, query, after, before).Scan(&count)
	return count > 0, err
}

// loadContextMessages reads the persisted context snapshot.
func loadContextMessages(db *sql.DB) ([]llm.Message, error) {
	var data string
	err := db.QueryRow(`SELECT messages FROM context_snapshot WHERE id = 1`).Scan(&data)
	if err != nil {
		return nil, err
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(data), &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

type affinityDelta struct {
	platformUserID string
	platform       string
	axis           string // "closeness" | "trust" | "interest"
	delta          float64
	reason         string
}

const evalPrompt = `あなたはassistant（ボット）の視点です。以下の会話を読んで、assistantから見た各ユーザーへの好感度の変化を評価してください。
assistant自身への評価はしないでください。userロールの人間だけが評価対象です。

フォーマット:
- [delta] user_id=<id> platform=<platform> axis=<closeness|trust|interest> delta=<+/-float> reason=<(感情) 日本語で簡潔に>

各軸の意味:
- closeness: 親密度。日常的なやり取り、共有体験で変動
- trust: 信頼度。秘密の共有、約束を守る/裏切りで変動
- interest: 関心度。面白い話題、知的刺激で変動

ルール:
- ポジティブなやり取り → +0.1 〜 +1.0
- ネガティブなやり取り → -0.1 〜 -1.0
- 事務的・中立的な会話 → 変化なし
- 1ユーザーにつき変動した軸のみ記載。複数軸が変動した場合は軸ごとに1行
- 変化がなければ「変化なし」とだけ返してください

会話:`

func evaluateAffinity(ctx context.Context, cc *scheduler.CronContext, msgs []llm.Message) ([]affinityDelta, error) {
	var sb strings.Builder
	sb.WriteString(evalPrompt)
	sb.WriteString("\n")
	for _, m := range msgs {
		if m.UserID != "" {
			fmt.Fprintf(&sb, "[%s] (user_id=%s, platform=%s, name=%s): %s\n",
				m.Role, m.UserID, m.Source, m.UserName, m.Content)
		} else {
			fmt.Fprintf(&sb, "[%s]: %s\n", m.Role, m.Content)
		}
	}

	resp, err := cc.LLM.CompleteRawDefault(ctx, []providers.Message{
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		return nil, err
	}

	return parseDeltas(resp.Text), nil
}

func parseDeltas(text string) []affinityDelta {
	if strings.Contains(text, "変化なし") {
		return nil
	}
	var deltas []affinityDelta
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		if !strings.HasPrefix(line, "[delta]") {
			continue
		}
		line = strings.TrimPrefix(line, "[delta]")
		line = strings.TrimSpace(line)

		d := affinityDelta{axis: "closeness"} // default axis
		parts := strings.Fields(line)
		for i, part := range parts {
			switch {
			case strings.HasPrefix(part, "user_id="):
				d.platformUserID = strings.TrimPrefix(part, "user_id=")
			case strings.HasPrefix(part, "platform="):
				d.platform = strings.TrimPrefix(part, "platform=")
			case strings.HasPrefix(part, "axis="):
				axis := strings.TrimPrefix(part, "axis=")
				switch axis {
				case "closeness", "trust", "interest":
					d.axis = axis
				}
			case strings.HasPrefix(part, "delta="):
				if f, err := strconv.ParseFloat(strings.TrimPrefix(part, "delta="), 64); err == nil {
					d.delta = f
				}
			case strings.HasPrefix(part, "reason="):
				reasonParts := append([]string{strings.TrimPrefix(part, "reason=")}, parts[i+1:]...)
				d.reason = strings.Join(reasonParts, " ")
				goto done
			}
		}
	done:
		if d.platformUserID != "" && d.delta != 0 {
			deltas = append(deltas, d)
		}
	}
	return deltas
}

// applyDelta resolves the platform user to an internal ID and updates affinity.
func applyDelta(ctx context.Context, users user.Store, d affinityDelta) error {
	u, err := users.ResolveExisting(ctx, d.platform, d.platformUserID)
	if err != nil {
		return fmt.Errorf("ユーザー %s/%s の解決に失敗: %w", d.platform, d.platformUserID, err)
	}

	axis := user.AffinityAxis(d.axis)
	if axis == "" {
		axis = user.AxisCloseness
	}

	return users.UpdateAffinity(ctx, &user.AffinityEvent{
		UserID: u.ID,
		Delta:  d.delta,
		Axis:   axis,
		Reason: d.reason,
	})
}
