-- +goose Up
CREATE TABLE IF NOT EXISTS context_snapshot (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  messages   TEXT NOT NULL DEFAULT '[]',
  updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- +goose Down
DROP TABLE IF EXISTS context_snapshot;
