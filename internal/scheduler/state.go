package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// LoadState reads the persisted state for the given task name into dest.
// If no state exists, dest is left untouched and nil is returned.
func LoadState(ctx context.Context, db *sql.DB, taskName string, dest any) error {
	var raw string
	err := db.QueryRowContext(ctx,
		`SELECT state FROM task_state WHERE task_name = ?`, taskName,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("タスク状態 %q の読み込みに失敗: %w", taskName, err)
	}
	return json.Unmarshal([]byte(raw), dest)
}

// SaveState persists the state for the given task name.
// It uses INSERT OR REPLACE to upsert.
func SaveState(ctx context.Context, db *sql.DB, taskName string, src any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("タスク状態 %q のマーシャルに失敗: %w", taskName, err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO task_state (task_name, state, updated_at)
		 VALUES (?, ?, datetime('now'))
		 ON CONFLICT(task_name) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
		taskName, string(data),
	)
	if err != nil {
		return fmt.Errorf("タスク状態 %q の保存に失敗: %w", taskName, err)
	}
	return nil
}
