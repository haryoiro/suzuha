-- +goose Up
CREATE TABLE IF NOT EXISTS memories (
	id         TEXT PRIMARY KEY,
	type       TEXT NOT NULL,
	content    TEXT NOT NULL,
	embedding  BLOB,
	metadata   TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);

-- +goose Down
DROP INDEX IF EXISTS idx_memories_type;
DROP TABLE IF EXISTS memories;
