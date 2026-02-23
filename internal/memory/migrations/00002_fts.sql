-- +goose Up
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
	content,
	content_rowid='rowid',
	tokenize='unicode61'
);

-- +goose Down
DROP TABLE IF EXISTS memories_fts;
