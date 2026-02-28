-- +goose Up
CREATE TABLE IF NOT EXISTS conversation_logs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  turn_id      TEXT NOT NULL,
  channel_id   TEXT NOT NULL,
  role         TEXT NOT NULL,
  content      TEXT NOT NULL,
  user_id      TEXT,
  user_name    TEXT,
  message_id   TEXT,
  tool_calls   TEXT,
  tool_call_id TEXT,
  timestamp    DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_convlogs_channel ON conversation_logs(channel_id);
CREATE INDEX idx_convlogs_turn ON conversation_logs(turn_id);
CREATE INDEX idx_convlogs_ts ON conversation_logs(timestamp);

-- +goose Down
DROP INDEX IF EXISTS idx_convlogs_ts;
DROP INDEX IF EXISTS idx_convlogs_turn;
DROP INDEX IF EXISTS idx_convlogs_channel;
DROP TABLE IF EXISTS conversation_logs;
