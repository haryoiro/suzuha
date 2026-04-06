-- +goose Up
CREATE TABLE context_snapshot_new (
  source_key TEXT PRIMARY KEY,
  messages   TEXT NOT NULL DEFAULT '[]',
  updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO context_snapshot_new (source_key, messages, updated_at)
  SELECT 'discord', messages, updated_at FROM context_snapshot WHERE id = 1;
DROP TABLE context_snapshot;
ALTER TABLE context_snapshot_new RENAME TO context_snapshot;

-- +goose Down
CREATE TABLE context_snapshot_old (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  messages   TEXT NOT NULL DEFAULT '[]',
  updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO context_snapshot_old (id, messages, updated_at)
  SELECT 1, messages, updated_at FROM context_snapshot WHERE source_key = 'discord';
DROP TABLE context_snapshot;
ALTER TABLE context_snapshot_old RENAME TO context_snapshot;
