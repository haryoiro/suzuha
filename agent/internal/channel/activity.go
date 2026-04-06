package channel

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ActivityStore provides read access to channel activity data.
type ActivityStore interface {
	// LastInteractionGlobal returns the most recent user message time across all channels,
	// and the channel it occurred in. Returns zero time if no activity exists.
	LastInteractionGlobal(ctx context.Context) (lastMsg time.Time, channelID string, err error)
}

// SQLiteActivityStore implements ActivityStore using the shared SQLite database.
type SQLiteActivityStore struct {
	db *sql.DB
}

// NewActivityStore creates a new SQLiteActivityStore.
func NewActivityStore(db *sql.DB) *SQLiteActivityStore {
	return &SQLiteActivityStore{db: db}
}

func (s *SQLiteActivityStore) LastInteractionGlobal(ctx context.Context) (time.Time, string, error) {
	var lastMsg time.Time
	var channelID string
	err := s.db.QueryRowContext(ctx,
		`SELECT channel_id, last_user_message_at
		 FROM channel_activity
		 WHERE last_user_message_at IS NOT NULL
		 ORDER BY last_user_message_at DESC
		 LIMIT 1`,
	).Scan(&channelID, &lastMsg)
	if err == sql.ErrNoRows {
		return time.Time{}, "", nil
	}
	if err != nil {
		return time.Time{}, "", fmt.Errorf("channel: 全チャンネルの最終インタラクション取得に失敗: %w", err)
	}
	return lastMsg, channelID, nil
}
