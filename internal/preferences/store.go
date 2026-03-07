package preferences

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Stance represents the bot's attitude toward a topic.
type Stance string

const (
	StanceLiked     Stance = "liked"
	StanceDisliked  Stance = "disliked"
	StanceCurious   Stance = "curious"
	StanceUndecided Stance = "undecided"
)

// Preference is a single value/interest entry.
type Preference struct {
	ID              int64     `json:"id"`
	Category        string    `json:"category"`
	Topic           string    `json:"topic"`
	Stance          Stance    `json:"stance"`
	Confidence      float64   `json:"confidence"`
	Reasoning       string    `json:"reasoning"`
	Encounters      int       `json:"encounters"`
	Shared          bool      `json:"shared"`
	LastEvaluatedAt time.Time `json:"last_evaluated_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Store manages preferences persistence.
type Store struct {
	db *sql.DB
}

// NewStore creates a new preferences store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Upsert inserts or updates a preference by topic.
// On conflict (same topic), it increments encounters and updates fields.
func (s *Store) Upsert(ctx context.Context, p *Preference) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO preferences (category, topic, stance, confidence, reasoning, encounters, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, datetime('now'))
		ON CONFLICT(topic) DO UPDATE SET
			category   = CASE WHEN excluded.category != '' THEN excluded.category ELSE preferences.category END,
			stance     = excluded.stance,
			confidence = excluded.confidence,
			reasoning  = excluded.reasoning,
			encounters = preferences.encounters + 1,
			updated_at = datetime('now')`,
		p.Category, p.Topic, string(p.Stance), p.Confidence, p.Reasoning)
	if err != nil {
		return fmt.Errorf("preferences upsert: %w", err)
	}
	return nil
}

// MarkEvaluated updates the last_evaluated_at timestamp.
func (s *Store) MarkEvaluated(ctx context.Context, id int64, stance Stance, confidence float64, reasoning string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE preferences
		SET stance = ?, confidence = ?, reasoning = ?, last_evaluated_at = datetime('now'), updated_at = datetime('now')
		WHERE id = ?`,
		string(stance), confidence, reasoning, id)
	if err != nil {
		return fmt.Errorf("preferences mark evaluated: %w", err)
	}
	return nil
}

// MarkShared sets the shared flag.
func (s *Store) MarkShared(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE preferences SET shared = 1, updated_at = datetime('now') WHERE id = ?`, id)
	return err
}

// ListPending returns preferences that need evaluation (low confidence or never evaluated).
func (s *Store) ListPending(ctx context.Context, limit int) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, topic, stance, confidence, reasoning, encounters, shared,
		       COALESCE(last_evaluated_at, ''), created_at, updated_at
		FROM preferences
		WHERE confidence < 0.7 OR last_evaluated_at IS NULL
		ORDER BY encounters DESC, created_at ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("preferences list pending: %w", err)
	}
	defer rows.Close()
	return scanPreferences(rows)
}

// ListConfident returns high-confidence preferences suitable for sharing or deepening.
func (s *Store) ListConfident(ctx context.Context, minConfidence float64, limit int) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, topic, stance, confidence, reasoning, encounters, shared,
		       COALESCE(last_evaluated_at, ''), created_at, updated_at
		FROM preferences
		WHERE confidence >= ?
		ORDER BY confidence DESC, encounters DESC
		LIMIT ?`, minConfidence, limit)
	if err != nil {
		return nil, fmt.Errorf("preferences list confident: %w", err)
	}
	defer rows.Close()
	return scanPreferences(rows)
}

// ListUnshareable returns confident but not-yet-shared preferences.
func (s *Store) ListUnshared(ctx context.Context, minConfidence float64, limit int) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, topic, stance, confidence, reasoning, encounters, shared,
		       COALESCE(last_evaluated_at, ''), created_at, updated_at
		FROM preferences
		WHERE confidence >= ? AND shared = 0 AND stance = 'liked'
		ORDER BY confidence DESC
		LIMIT ?`, minConfidence, limit)
	if err != nil {
		return nil, fmt.Errorf("preferences list unshared: %w", err)
	}
	defer rows.Close()
	return scanPreferences(rows)
}

// ListAll returns all preferences.
func (s *Store) ListAll(ctx context.Context, limit int) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, topic, stance, confidence, reasoning, encounters, shared,
		       COALESCE(last_evaluated_at, ''), created_at, updated_at
		FROM preferences
		ORDER BY updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("preferences list all: %w", err)
	}
	defer rows.Close()
	return scanPreferences(rows)
}

// ListByStance returns preferences filtered by stance.
func (s *Store) ListByStance(ctx context.Context, stance Stance, limit int) ([]Preference, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, category, topic, stance, confidence, reasoning, encounters, shared,
		       COALESCE(last_evaluated_at, ''), created_at, updated_at
		FROM preferences
		WHERE stance = ?
		ORDER BY confidence DESC
		LIMIT ?`, string(stance), limit)
	if err != nil {
		return nil, fmt.Errorf("preferences list by stance: %w", err)
	}
	defer rows.Close()
	return scanPreferences(rows)
}

// Delete removes a preference by ID.
func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM preferences WHERE id = ?`, id)
	return err
}

func scanPreferences(rows *sql.Rows) ([]Preference, error) {
	var prefs []Preference
	for rows.Next() {
		var p Preference
		var evalAt string
		if err := rows.Scan(
			&p.ID, &p.Category, &p.Topic, &p.Stance, &p.Confidence,
			&p.Reasoning, &p.Encounters, &p.Shared,
			&evalAt, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("preferences scan: %w", err)
		}
		if evalAt != "" {
			p.LastEvaluatedAt, _ = time.Parse("2006-01-02 15:04:05", evalAt)
		}
		prefs = append(prefs, p)
	}
	return prefs, rows.Err()
}
