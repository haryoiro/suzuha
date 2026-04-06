-- +goose Up
CREATE TABLE IF NOT EXISTS channel_activity (
  channel_id TEXT PRIMARY KEY,
  last_user_message_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS channel_activity;
