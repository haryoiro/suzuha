package affinity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
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
		cc.Logger.Warn("affinity_eval: load state", "error", err)
		return nil
	}
	t.lastEvaluatedAt = s.LastEvaluatedAt
	cc.Logger.Info("affinity_eval: restored state", "last_evaluated_at", s.LastEvaluatedAt)
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
		cc.Logger.Debug("affinity_eval: check activity", "error", err)
		return nil
	}
	if !hasRecentActivity {
		cc.Logger.Debug("affinity_eval: no recent activity to evaluate")
		return nil
	}

	// 2. Load conversation messages from context_snapshot.
	msgs, err := loadContextMessages(cc.DB)
	if err != nil {
		cc.Logger.Debug("affinity_eval: load context", "error", err)
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
		cc.Logger.Debug("affinity_eval: too few messages", "count", len(recent))
		return nil
	}

	// 4. Run lightweight LLM evaluation.
	deltas, err := evaluateAffinity(ctx, cc, recent)
	if err != nil {
		cc.Logger.Error("affinity_eval: llm eval", "error", err)
		return nil
	}

	// 5. Apply deltas.
	for _, d := range deltas {
		if err := applyDelta(ctx, cc.DB, d); err != nil {
			cc.Logger.Warn("affinity_eval: apply delta", "error", err, "user_id", d.platformUserID)
		} else {
			cc.Logger.Info("affinity_eval: applied",
				"user_id", d.platformUserID, "delta", d.delta, "reason", d.reason)
		}
	}

	// 6. Update state.
	now := time.Now()
	t.lastEvaluatedAt = now
	if saveErr := scheduler.SaveState(ctx, cc.DB, t.Name(), &persistedState{LastEvaluatedAt: now}); saveErr != nil {
		cc.Logger.Warn("affinity_eval: save state", "error", saveErr)
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
	delta          float64
	reason         string
}

const evalPrompt = `以下の短い会話から、各ユーザーに対する好感度の変化を評価してください。

フォーマット:
- [delta] user_id=<id> platform=<platform> delta=<+/-float> reason=<日本語で簡潔に>

ルール:
- ポジティブなやり取り（感謝、楽しさ、共感）→ +0.1 〜 +1.0
- ネガティブなやり取り（敵意、無礼）→ -0.1 〜 -1.0
- 事務的・中立的な会話 → 変化なし
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

	resp, err := cc.LLM.CompleteRaw(ctx, []providers.Message{
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

		var d affinityDelta
		parts := strings.Fields(line)
		for i, part := range parts {
			switch {
			case strings.HasPrefix(part, "user_id="):
				d.platformUserID = strings.TrimPrefix(part, "user_id=")
			case strings.HasPrefix(part, "platform="):
				d.platform = strings.TrimPrefix(part, "platform=")
			case strings.HasPrefix(part, "delta="):
				if f, err := strconv.ParseFloat(strings.TrimPrefix(part, "delta="), 64); err == nil {
					d.delta = f
				}
			case strings.HasPrefix(part, "reason="):
				reasonParts := append([]string{strings.TrimPrefix(part, "reason=")}, parts[i+1:]...)
				d.reason = strings.Join(reasonParts, " ")
				break
			}
		}
		if d.platformUserID != "" && d.delta != 0 {
			deltas = append(deltas, d)
		}
	}
	return deltas
}

// applyDelta resolves the platform user to an internal ID and updates affinity.
func applyDelta(ctx context.Context, db *sql.DB, d affinityDelta) error {
	// Resolve platform user → internal user ID.
	var userID string
	err := db.QueryRowContext(ctx,
		`SELECT user_id FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
		d.platform, d.platformUserID,
	).Scan(&userID)
	if err != nil {
		return fmt.Errorf("resolve user %s/%s: %w", d.platform, d.platformUserID, err)
	}

	// Atomic: insert event + update affinity.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	eventID := uuid.NewString()
	now := time.Now()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO affinity_events (id, user_id, delta, reason, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		eventID, userID, d.delta, d.reason, now)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE users SET affinity = affinity + ?, updated_at = ? WHERE id = ?`,
		d.delta, now, userID)
	if err != nil {
		return fmt.Errorf("update affinity: %w", err)
	}

	return tx.Commit()
}
