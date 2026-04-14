package action

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/robfig/cron/v3"
)

// Action represents a scheduled action row.
type Action struct {
	ID            string
	ChannelID     string
	Content       string
	Mode          string // "direct" (post as-is) or "prompt" (LLM generates response)
	ScheduledAt   time.Time
	CronExpr      string // empty for one-shot
	RandomMinutes int    // random offset window in minutes (0 = disabled)
	CreatedBy     string
	Status        string // pending, executed, canceled, failed
	RetryCount    int
	ExecutedAt    *time.Time
	CreatedAt     time.Time
}

// Store handles scheduled_actions DB operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Setup creates the table if it doesn't exist (idempotent).
func (s *Store) Setup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS scheduled_actions (
			id             TEXT PRIMARY KEY,
			channel_id     TEXT NOT NULL,
			content        TEXT NOT NULL,
			scheduled_at   TIMESTAMPTZ NOT NULL,
			cron_expr      TEXT,
			random_minutes INTEGER NOT NULL DEFAULT 0,
			created_by     TEXT,
			status         TEXT NOT NULL DEFAULT 'pending',
			executed_at    TIMESTAMPTZ,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err == nil {
		// Add columns if upgrading from older schema (idempotent).
		s.db.ExecContext(ctx, `ALTER TABLE scheduled_actions ADD COLUMN random_minutes INTEGER NOT NULL DEFAULT 0`)
		s.db.ExecContext(ctx, `ALTER TABLE scheduled_actions ADD COLUMN mode TEXT NOT NULL DEFAULT 'direct'`)
		s.db.ExecContext(ctx, `ALTER TABLE scheduled_actions ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`)
	}
	if err != nil {
		return fmt.Errorf("scheduled_actions テーブルの作成に失敗: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_scheduled_actions_due
		ON scheduled_actions (status, scheduled_at)`)
	if err != nil {
		return fmt.Errorf("schedule: インデックス作成に失敗: %w", err)
	}
	return nil
}

// Create inserts a new scheduled action with auto-generated UUID.
func (s *Store) Create(ctx context.Context, a *Action) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	mode := a.Mode
	if mode == "" {
		mode = "direct"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduled_actions (id, channel_id, content, scheduled_at, cron_expr, random_minutes, created_by, mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.ChannelID, a.Content, a.ScheduledAt.UTC().Format(time.RFC3339), nullString(a.CronExpr), a.RandomMinutes, nullString(a.CreatedBy), mode,
	)
	if err != nil {
		return fmt.Errorf("schedule: アクションの作成に失敗: %w", err)
	}
	return nil
}

// ListPending returns all pending actions ordered by scheduled_at ASC.
func (s *Store) ListPending(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, content, COALESCE(mode,'direct'), scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, retry_count, executed_at, created_at
		FROM scheduled_actions
		WHERE status = 'pending'
		ORDER BY scheduled_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// ListPendingByCreator returns pending actions filtered by created_by.
func (s *Store) ListPendingByCreator(ctx context.Context, createdBy string) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, content, COALESCE(mode,'direct'), scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, retry_count, executed_at, created_at
		FROM scheduled_actions
		WHERE status = 'pending' AND created_by = $1
		ORDER BY scheduled_at ASC`, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// Cancel marks a pending action as canceled. Returns false if not found or not pending.
func (s *Store) Cancel(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET status = 'cancelled'
		WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("schedule: cancel rows affected: %w", err)
	}
	return n > 0, nil
}

// FetchDue returns pending actions whose scheduled_at <= now.
func (s *Store) FetchDue(ctx context.Context, now time.Time) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, content, COALESCE(mode,'direct'), scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, retry_count, executed_at, created_at
		FROM scheduled_actions
		WHERE status = 'pending' AND scheduled_at <= $1
		ORDER BY scheduled_at ASC`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// MarkExecuted marks an action as executed.
// For recurring actions (cron_expr set), it reschedules to the next occurrence instead.
func (s *Store) MarkExecuted(ctx context.Context, id string, now time.Time) error {
	// First, get the action to check if it's recurring.
	var cronExpr string
	var randomMinutes int
	var scheduledAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(cron_expr,''), random_minutes, scheduled_at FROM scheduled_actions WHERE id = $1`, id,
	).Scan(&cronExpr, &randomMinutes, &scheduledAt)
	if err != nil {
		return fmt.Errorf("アクション %s の取得に失敗: %w", id, err)
	}

	if cronExpr != "" {
		// Recurring: compute next execution time and reschedule.
		next, parseErr := nextCronTime(cronExpr, now)
		if parseErr != nil {
			// Cron expression invalid — mark executed and stop recurring.
			return s.markDone(ctx, id, now)
		}
		// Apply random offset if configured.
		if randomMinutes > 0 {
			offset := time.Duration(rand.IntN(randomMinutes)) * time.Minute
			next = next.Add(offset)
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE scheduled_actions SET scheduled_at = $1, executed_at = $2
			WHERE id = $3`, next.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339), id)
		if err != nil {
			return fmt.Errorf("schedule: 次回スケジュールの更新に失敗: %w", err)
		}
		return nil
	}

	return s.markDone(ctx, id, now)
}

func (s *Store) markDone(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET status = 'executed', executed_at = $1
		WHERE id = $2`, now.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("schedule: 実行済みへの更新に失敗: %w", err)
	}
	return nil
}

// nextCronTime parses a cron expression and returns the next occurrence after t.
// The time is interpreted in the configured timezone.
func nextCronTime(expr string, t time.Time) (time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron式 %q の解析に失敗: %w", expr, err)
	}
	return sched.Next(jtime.In(t)), nil
}

func scanActions(rows *sql.Rows) ([]Action, error) {
	var actions []Action
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.Content, &a.Mode, &a.ScheduledAt, &a.CronExpr, &a.RandomMinutes, &a.CreatedBy, &a.Status, &a.RetryCount, &a.ExecutedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

// ActionListOpts controls filtering for admin List.
type ActionListOpts struct {
	Status string // empty = all
	Limit  int
}

// ActionUpdateFields holds the optional update fields for admin Update.
type ActionUpdateFields struct {
	ChannelID   *string
	Content     *string
	Mode        *string
	ScheduledAt *string
	CronExpr    *string
	Status      *string
}

// List returns actions with optional status filter, ordered by scheduled_at DESC.
func (s *Store) List(ctx context.Context, opts ActionListOpts) ([]Action, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	query := `SELECT id, channel_id, content, COALESCE(mode,'direct'), scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, retry_count, executed_at, created_at
	          FROM scheduled_actions`
	var args []any
	n := 1
	if opts.Status != "" {
		query += fmt.Sprintf(` WHERE status = $%d`, n)
		args = append(args, opts.Status)
		n++
	}
	query += fmt.Sprintf(` ORDER BY scheduled_at DESC LIMIT $%d`, n)
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("schedule: 一覧取得に失敗: %w", err)
	}
	defer rows.Close()
	return scanActions(rows)
}

// Update applies partial updates to a scheduled action.
func (s *Store) Update(ctx context.Context, id string, fields ActionUpdateFields) error {
	var sets []string
	var args []any
	n := 1
	if fields.ChannelID != nil {
		sets = append(sets, fmt.Sprintf("channel_id = $%d", n))
		args = append(args, *fields.ChannelID)
		n++
	}
	if fields.Content != nil {
		sets = append(sets, fmt.Sprintf("content = $%d", n))
		args = append(args, *fields.Content)
		n++
	}
	if fields.Mode != nil {
		sets = append(sets, fmt.Sprintf("mode = $%d", n))
		args = append(args, *fields.Mode)
		n++
	}
	if fields.ScheduledAt != nil {
		sets = append(sets, fmt.Sprintf("scheduled_at = $%d", n))
		args = append(args, *fields.ScheduledAt)
		n++
	}
	if fields.CronExpr != nil {
		sets = append(sets, fmt.Sprintf("cron_expr = $%d", n))
		args = append(args, *fields.CronExpr)
		n++
	}
	if fields.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", n))
		args = append(args, *fields.Status)
		n++
	}
	if len(sets) == 0 {
		return fmt.Errorf("schedule: 更新するフィールドがありません")
	}
	args = append(args, id)

	query := "UPDATE scheduled_actions SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += fmt.Sprintf(" WHERE id = $%d", n)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("schedule: 更新に失敗: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("schedule: update rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("schedule: 見つかりません: %s", id)
	}
	return nil
}

// Delete removes a scheduled action by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_actions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("schedule: 削除に失敗: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("schedule: delete rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("schedule: 見つかりません: %s", id)
	}
	return nil
}

// IncrRetry increments the retry count for a pending action.
func (s *Store) IncrRetry(ctx context.Context, id string, count int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET retry_count = $1 WHERE id = $2`, count, id)
	if err != nil {
		return fmt.Errorf("schedule: リトライ回数の更新に失敗: %w", err)
	}
	return nil
}

// MarkFailed marks an action as failed after exceeding retry limit.
func (s *Store) MarkFailed(ctx context.Context, id string, count int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET status = 'failed', retry_count = $1 WHERE id = $2`, count, id)
	if err != nil {
		return fmt.Errorf("schedule: 失敗ステータスへの更新に失敗: %w", err)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
