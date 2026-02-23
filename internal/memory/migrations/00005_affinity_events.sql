-- +goose Up
CREATE TABLE IF NOT EXISTS affinity_events (
	id              TEXT PRIMARY KEY,
	user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	delta           REAL NOT NULL,
	reason          TEXT NOT NULL DEFAULT '',
	interaction_ids TEXT,
	group_start     DATETIME,
	group_end       DATETIME,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_affinity_events_user ON affinity_events(user_id);
CREATE INDEX IF NOT EXISTS idx_affinity_events_time ON affinity_events(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_affinity_events_time;
DROP INDEX IF EXISTS idx_affinity_events_user;
DROP TABLE IF EXISTS affinity_events;
