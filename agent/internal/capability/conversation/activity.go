package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ActivityStore はチャンネル活動データへの read アクセスを提供する。
type ActivityStore interface {
	// LastInteractionGlobal は全チャンネルで最も新しいユーザーメッセージの時刻と
	// それが発生したチャンネルを返す。活動がなければ zero time を返す。
	LastInteractionGlobal(ctx context.Context) (lastMsg time.Time, channelID string, err error)
}

// DBActivityStore は共有 DB を使った ActivityStore 実装。
type DBActivityStore struct {
	db *sql.DB
}

// NewActivityStore は DBActivityStore を生成する。
func NewActivityStore(db *sql.DB) *DBActivityStore {
	return &DBActivityStore{db: db}
}

// LastInteractionGlobal は全チャンネルで最も新しいユーザーメッセージを返す。
func (s *DBActivityStore) LastInteractionGlobal(ctx context.Context) (time.Time, string, error) {
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
		return time.Time{}, "", fmt.Errorf("conversation: 全チャンネルの最終インタラクション取得に失敗: %w", err)
	}
	return lastMsg, channelID, nil
}
