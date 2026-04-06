-- +goose Up
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
	content,
	tokenize='trigram'
);

-- +goose Down
DROP TABLE IF EXISTS memories_fts;
