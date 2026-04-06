package diary

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Entry は日記エントリ（hourly digest または daily diary）。
type Entry struct {
	ID          string
	Kind        string // "hourly" or "daily"
	Content     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

// Store は diary_entries テーブルへのアクセスを提供する。
type Store struct {
	db *sql.DB
}

// NewStore は新しい diary Store を作成する。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Save は日記エントリを保存する。
func (s *Store) Save(ctx context.Context, e *Entry) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO diary_entries (id, kind, content, period_start, period_end, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		e.ID, e.Kind, e.Content, e.PeriodStart, e.PeriodEnd, e.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("diary: 保存に失敗: %w", err)
	}
	return nil
}

// ListByKind は指定 kind の日記エントリを period_start 降順で返す。
// kind が空文字列の場合は全 kind を返す。
func (s *Store) ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]Entry, error) {
	var q string
	var args []any
	if kind != "" {
		q = `SELECT id, kind, content, period_start, period_end, created_at
		     FROM diary_entries WHERE kind = $1 AND period_start >= $2
		     ORDER BY period_start DESC LIMIT $3`
		args = []any{kind, since, limit}
	} else {
		q = `SELECT id, kind, content, period_start, period_end, created_at
		     FROM diary_entries WHERE period_start >= $1
		     ORDER BY period_start DESC LIMIT $2`
		args = []any{since, limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("diary: 一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Content, &e.PeriodStart, &e.PeriodEnd, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
