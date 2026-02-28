package schedule

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Action represents a scheduled action row.
type Action struct {
	ID            string
	ChannelID     string
	Content       string
	ScheduledAt   time.Time
	CronExpr      string // empty for one-shot
	RandomMinutes int    // random offset window in minutes (0 = disabled)
	CreatedBy     string
	Status        string // pending, executed, cancelled
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
			scheduled_at   DATETIME NOT NULL,
			cron_expr      TEXT,
			random_minutes INTEGER NOT NULL DEFAULT 0,
			created_by     TEXT,
			status         TEXT NOT NULL DEFAULT 'pending',
			executed_at    DATETIME,
			created_at     DATETIME NOT NULL DEFAULT (datetime('now'))
		)`)
	if err == nil {
		// Add column if upgrading from older schema (idempotent).
		s.db.ExecContext(ctx, `ALTER TABLE scheduled_actions ADD COLUMN random_minutes INTEGER NOT NULL DEFAULT 0`)
	}
	if err != nil {
		return fmt.Errorf("create scheduled_actions: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_scheduled_actions_due
		ON scheduled_actions (status, scheduled_at)`)
	return err
}

// Create inserts a new scheduled action with auto-generated UUID.
func (s *Store) Create(ctx context.Context, a *Action) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scheduled_actions (id, channel_id, content, scheduled_at, cron_expr, random_minutes, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ChannelID, a.Content, a.ScheduledAt.UTC(), nullString(a.CronExpr), a.RandomMinutes, nullString(a.CreatedBy),
	)
	return err
}

// ListPending returns all pending actions ordered by scheduled_at ASC.
func (s *Store) ListPending(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, content, scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, executed_at, created_at
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
		SELECT id, channel_id, content, scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, executed_at, created_at
		FROM scheduled_actions
		WHERE status = 'pending' AND created_by = ?
		ORDER BY scheduled_at ASC`, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanActions(rows)
}

// Cancel marks a pending action as cancelled. Returns false if not found or not pending.
func (s *Store) Cancel(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET status = 'cancelled'
		WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// FetchDue returns pending actions whose scheduled_at <= now.
func (s *Store) FetchDue(ctx context.Context, now time.Time) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, channel_id, content, scheduled_at, COALESCE(cron_expr,''), random_minutes, COALESCE(created_by,''), status, executed_at, created_at
		FROM scheduled_actions
		WHERE status = 'pending' AND scheduled_at <= ?
		ORDER BY scheduled_at ASC`, now.UTC())
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
		SELECT COALESCE(cron_expr,''), random_minutes, scheduled_at FROM scheduled_actions WHERE id = ?`, id,
	).Scan(&cronExpr, &randomMinutes, &scheduledAt)
	if err != nil {
		return fmt.Errorf("fetch action %s: %w", id, err)
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
			UPDATE scheduled_actions SET scheduled_at = ?, executed_at = ?
			WHERE id = ?`, next.UTC(), now.UTC(), id)
		return err
	}

	return s.markDone(ctx, id, now)
}

func (s *Store) markDone(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scheduled_actions SET status = 'executed', executed_at = ?
		WHERE id = ?`, now.UTC(), id)
	return err
}

// nextCronTime parses a cron expression and returns the next occurrence after t.
func nextCronTime(expr string, t time.Time) (time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", expr, err)
	}
	return sched.Next(t), nil
}

func scanActions(rows *sql.Rows) ([]Action, error) {
	var actions []Action
	for rows.Next() {
		var a Action
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.Content, &a.ScheduledAt, &a.CronExpr, &a.RandomMinutes, &a.CreatedBy, &a.Status, &a.ExecutedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
