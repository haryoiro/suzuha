package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/message"
	portconv "github.com/haryoiro/suzuha/internal/port/conversation"
)

// LogRow は conversation_logs から読み出した行 (adapter 内部の値型)。
type LogRow struct {
	SourceKey string
	ChannelID string
	Role      string
	UserName  string
	Content   string
	Timestamp time.Time
}

// DBStore は port/conversation.Store の SQL 実装。
type DBStore struct {
	db *sql.DB
}

// NewDBStore は DBStore を生成する。
func NewDBStore(db *sql.DB) *DBStore {
	return &DBStore{db: db}
}

// LogTurn は会話ターンの 1 メッセージを記録する。
func (s *DBStore) LogTurn(ctx context.Context, entry portconv.TurnEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversation_logs (turn_id, channel_id, role, content, user_id, user_name, message_id, tool_calls, tool_call_id, timestamp, source_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		entry.TurnID, entry.ChannelID, entry.Role, entry.Content,
		nullIfEmpty(entry.UserID), nullIfEmpty(entry.UserName), nullIfEmpty(entry.MessageID),
		nullIfEmpty(entry.ToolCalls), nullIfEmpty(entry.ToolCallID),
		entry.Timestamp, entry.SourceKey,
	)
	return err
}

// ListLogs は指定期間の会話ログを返す。
func (s *DBStore) ListLogs(ctx context.Context, from, to time.Time) ([]LogRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_key, channel_id, role, COALESCE(user_name, ''), content, timestamp
		 FROM conversation_logs
		 WHERE timestamp >= $1 AND timestamp < $2
		   AND role IN ('user', 'assistant')
		 ORDER BY timestamp ASC`,
		from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("conversation: ログ取得に失敗: %w", err)
	}
	defer rows.Close()

	var result []LogRow
	for rows.Next() {
		var r LogRow
		if err := rows.Scan(&r.SourceKey, &r.ChannelID, &r.Role, &r.UserName, &r.Content, &r.Timestamp); err != nil {
			continue
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// RecentAssistantMessages は最新の assistant メッセージを返す。
func (s *DBStore) RecentAssistantMessages(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT content FROM conversation_logs
		 WHERE role = 'assistant' AND content != ''
		 ORDER BY timestamp DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: assistant メッセージ取得に失敗: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			continue
		}
		result = append(result, content)
	}
	return result, rows.Err()
}

// TrackActivity はチャンネルの最終ユーザーメッセージ時刻を記録する。
func (s *DBStore) TrackActivity(ctx context.Context, channelID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_activity (channel_id, last_user_message_at) VALUES ($1, $2)
		 ON CONFLICT(channel_id) DO UPDATE SET last_user_message_at = EXCLUDED.last_user_message_at`,
		channelID, at)
	return err
}

// SaveSnapshot はコンテキストスナップショットを保存する。
func (s *DBStore) SaveSnapshot(ctx context.Context, sourceKey string, messages []message.Message) error {
	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("conversation: snapshot marshal に失敗: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO context_snapshot (source_key, messages, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (source_key) DO UPDATE SET messages = EXCLUDED.messages, updated_at = now()`,
		sourceKey, string(data))
	return err
}

// LoadSnapshot はコンテキストスナップショットを復元する。
func (s *DBStore) LoadSnapshot(ctx context.Context, sourceKey string) ([]message.Message, error) {
	var data string
	err := s.db.QueryRowContext(ctx,
		`SELECT messages FROM context_snapshot WHERE source_key = $1`, sourceKey,
	).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("conversation: snapshot load に失敗: %w", err)
	}
	var msgs []message.Message
	if err := json.Unmarshal([]byte(data), &msgs); err != nil {
		return nil, fmt.Errorf("conversation: snapshot unmarshal に失敗: %w", err)
	}
	return msgs, nil
}

// DeleteChannel はチャンネルに関連する全データを削除する。
func (s *DBStore) DeleteChannel(ctx context.Context, channelID string) error {
	tables := []string{"channel_settings", "channel_activity", "conversation_logs", "user_guild_channels"}
	for _, table := range tables {
		if _, err := s.db.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE channel_id = $1", table), channelID,
		); err != nil {
			return fmt.Errorf("conversation: %s 削除に失敗: %w", table, err)
		}
	}
	return nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
