-- +goose Up
ALTER TABLE memories ADD COLUMN keywords TEXT;
ALTER TABLE memories ADD COLUMN topic TEXT;
ALTER TABLE memories ADD COLUMN persons TEXT;
ALTER TABLE memories ADD COLUMN event_time DATETIME;
CREATE INDEX idx_memories_topic ON memories(topic);
CREATE INDEX idx_memories_event_time ON memories(event_time);

-- +goose Down
DROP INDEX IF EXISTS idx_memories_event_time;
DROP INDEX IF EXISTS idx_memories_topic;
