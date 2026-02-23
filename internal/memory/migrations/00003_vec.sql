-- +goose Up
-- sqlite-vec virtual table for vector search.
-- May fail if the extension is not loaded.
CREATE VIRTUAL TABLE IF NOT EXISTS memories_vec USING vec0(
	id TEXT PRIMARY KEY,
	embedding float[768]
);

-- +goose Down
DROP TABLE IF EXISTS memories_vec;
