-- +goose Up
CREATE TABLE IF NOT EXISTS task_state (
  task_name  TEXT PRIMARY KEY,
  state      TEXT NOT NULL DEFAULT '{}',
  updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE IF EXISTS task_state;
