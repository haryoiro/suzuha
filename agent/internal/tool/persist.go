package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// LoadDisabled は app_settings.disabled_tools から無効化ツール名リストを読む。
// レコードがない or 空の場合は nil, nil。
func LoadDisabled(ctx context.Context, db *sql.DB) ([]string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'disabled_tools'`).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tool: disabled_tools 読み込み失敗: %w", err)
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("tool: disabled_tools のパース失敗: %w", err)
	}
	return names, nil
}

// SaveDisabled は無効化ツール名リストを app_settings に upsert する。
func SaveDisabled(ctx context.Context, db *sql.DB, names []string) error {
	data, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("tool: disabled_tools marshal 失敗: %w", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO app_settings (key, value) VALUES ('disabled_tools', $1)
		 ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`,
		string(data),
	)
	if err != nil {
		return fmt.Errorf("tool: disabled_tools 保存失敗: %w", err)
	}
	return nil
}
