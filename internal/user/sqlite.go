package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SQLiteStore implements Store using the shared SQLite database.
type SQLiteStore struct {
	db         *sql.DB
	botUserIDs map[string]bool // platform user IDs that belong to the bot itself
}

// NewSQLiteStore creates a user store that shares the given database connection.
// botPlatformUserIDs are platform user IDs (e.g. Discord user ID) that identify
// the bot itself. Users resolved with these IDs are marked as is_bot=true.
func NewSQLiteStore(db *sql.DB, botPlatformUserIDs ...string) *SQLiteStore {
	ids := make(map[string]bool, len(botPlatformUserIDs))
	for _, id := range botPlatformUserIDs {
		if id != "" {
			ids[id] = true
		}
	}
	return &SQLiteStore{db: db, botUserIDs: ids}
}

// AddBotID registers an additional platform user ID as belonging to the bot.
// This is used when the actual bot ID is only known at runtime (e.g. after Discord connects).
func (s *SQLiteStore) AddBotID(platformUserID string) {
	if platformUserID != "" {
		s.botUserIDs[platformUserID] = true
	}
}

func (s *SQLiteStore) Resolve(ctx context.Context, platform, platformUserID, platformName string) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("user: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Look up existing platform link.
	var userID string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
		platform, platformUserID,
	).Scan(&userID)

	if err == nil {
		// User exists — load and return.
		u, err := s.getInTx(ctx, tx, userID)
		if err != nil {
			return nil, err
		}
		// If this is a known bot ID but the user wasn't marked yet, fix it.
		if s.botUserIDs[platformUserID] && !u.IsBot {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET is_bot = 1, updated_at = ? WHERE id = ?`,
				time.Now(), userID,
			); err != nil {
				return nil, fmt.Errorf("user: mark bot: %w", err)
			}
			u.IsBot = true
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("user: commit: %w", err)
		}
		return u, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("user: lookup platform link: %w", err)
	}

	// User does not exist — create.
	isBot := s.botUserIDs[platformUserID]
	role := RoleMember
	switch {
	case isBot:
		role = RoleMember // bot gets member role, identified by is_bot flag
	case platform == "cli":
		role = RoleOwner
	}

	now := time.Now()
	u := &User{
		ID:        uuid.NewString(),
		Role:      role,
		IsBot:     isBot,
		Affinity:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, display_name, role, is_bot, affinity, created_at, updated_at)
		 VALUES (?, '', ?, ?, 0.0, ?, ?)`,
		u.ID, string(u.Role), u.IsBot, u.CreatedAt, u.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("user: insert user: %w", err)
	}

	linkID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO platform_links (id, user_id, platform, platform_user_id, platform_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		linkID, u.ID, platform, platformUserID, platformName, now,
	); err != nil {
		return nil, fmt.Errorf("user: insert platform link: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("user: commit: %w", err)
	}
	return u, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*User, error) {
	return s.getFromDB(ctx, s.db, id)
}

func (s *SQLiteStore) getInTx(ctx context.Context, tx *sql.Tx, id string) (*User, error) {
	return s.getFromDB(ctx, tx, id)
}

type queryable interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLiteStore) getFromDB(ctx context.Context, q queryable, id string) (*User, error) {
	var u User
	var roleStr string
	var metaJSON sql.NullString

	err := q.QueryRowContext(ctx,
		`SELECT id, display_name, role, is_bot, affinity, metadata, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.DisplayName, &roleStr, &u.IsBot, &u.Affinity, &metaJSON, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("user: get: %w", err)
	}
	u.Role = Role(roleStr)
	if metaJSON.Valid {
		_ = json.Unmarshal([]byte(metaJSON.String), &u.Metadata)
	}
	return &u, nil
}

func (s *SQLiteStore) UpdateDisplayName(ctx context.Context, userID, displayName string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
		displayName, time.Now(), userID,
	)
	if err != nil {
		return fmt.Errorf("user: update display_name: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user: not found: %s", userID)
	}
	return nil
}

func (s *SQLiteStore) UpdateAffinity(ctx context.Context, evt *AffinityEvent) error {
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now()
	}

	interactionJSON, _ := json.Marshal(evt.InteractionIDs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Insert the affinity event.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO affinity_events (id, user_id, delta, reason, interaction_ids, group_start, group_end, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.ID, evt.UserID, evt.Delta, evt.Reason,
		string(interactionJSON),
		nullTime(evt.GroupStart), nullTime(evt.GroupEnd),
		evt.CreatedAt,
	); err != nil {
		return fmt.Errorf("user: insert affinity event: %w", err)
	}

	// Update the user's running affinity total.
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET affinity = affinity + ?, updated_at = ? WHERE id = ?`,
		evt.Delta, time.Now(), evt.UserID,
	); err != nil {
		return fmt.Errorf("user: update affinity: %w", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) GetAffinity(ctx context.Context, userID string, limit int) ([]AffinityEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, delta, reason, interaction_ids, group_start, group_end, created_at
		 FROM affinity_events WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("user: get affinity: %w", err)
	}
	defer rows.Close()

	var events []AffinityEvent
	for rows.Next() {
		var e AffinityEvent
		var idsJSON sql.NullString
		var groupStart, groupEnd sql.NullTime
		if err := rows.Scan(&e.ID, &e.UserID, &e.Delta, &e.Reason, &idsJSON, &groupStart, &groupEnd, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: scan affinity: %w", err)
		}
		if idsJSON.Valid {
			_ = json.Unmarshal([]byte(idsJSON.String), &e.InteractionIDs)
		}
		if groupStart.Valid {
			e.GroupStart = groupStart.Time
		}
		if groupEnd.Valid {
			e.GroupEnd = groupEnd.Time
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) Close() error {
	// DB is shared — don't close it here.
	return nil
}

// nullTime returns nil for zero time values (so SQLite stores NULL).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
