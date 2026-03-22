-- +goose Up
ALTER TABLE conversation_logs ADD COLUMN source_key TEXT NOT NULL DEFAULT 'discord';
CREATE INDEX idx_convlogs_source ON conversation_logs(source_key);

-- +goose Down
DROP INDEX IF EXISTS idx_convlogs_source;
ALTER TABLE conversation_logs DROP COLUMN source_key;
