-- +goose Up
CREATE TABLE IF NOT EXISTS scheduled_actions (
  id           TEXT PRIMARY KEY,
  channel_id   TEXT NOT NULL,
  content      TEXT NOT NULL,
  scheduled_at DATETIME NOT NULL,
  cron_expr    TEXT,
  created_by   TEXT,
  status       TEXT NOT NULL DEFAULT 'pending',
  executed_at  DATETIME,
  created_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_scheduled_actions_due
  ON scheduled_actions (status, scheduled_at);

-- +goose Down
DROP TABLE IF EXISTS scheduled_actions;
