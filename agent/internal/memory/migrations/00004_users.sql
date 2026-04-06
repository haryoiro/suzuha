-- +goose Up
CREATE TABLE IF NOT EXISTS users (
	id           TEXT PRIMARY KEY,
	display_name TEXT NOT NULL DEFAULT '',
	role         TEXT NOT NULL DEFAULT 'member',
	affinity     REAL NOT NULL DEFAULT 0.0,
	metadata     TEXT,
	created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS platform_links (
	id               TEXT PRIMARY KEY,
	user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	platform         TEXT NOT NULL,
	platform_user_id TEXT NOT NULL,
	platform_name    TEXT NOT NULL DEFAULT '',
	created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(platform, platform_user_id)
);

CREATE INDEX IF NOT EXISTS idx_platform_links_user ON platform_links(user_id);
CREATE INDEX IF NOT EXISTS idx_platform_links_lookup ON platform_links(platform, platform_user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_platform_links_lookup;
DROP INDEX IF EXISTS idx_platform_links_user;
DROP TABLE IF EXISTS platform_links;
DROP TABLE IF EXISTS users;
