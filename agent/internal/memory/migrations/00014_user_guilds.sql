-- +goose Up
CREATE TABLE IF NOT EXISTS guilds (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL DEFAULT '',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_guild_channels (
  user_id      TEXT NOT NULL,
  guild_id     TEXT NOT NULL,
  channel_id   TEXT NOT NULL,
  channel_name TEXT NOT NULL DEFAULT '',
  last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, guild_id, channel_id)
);

CREATE INDEX IF NOT EXISTS idx_ugc_user ON user_guild_channels(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_ugc_user;
DROP TABLE IF EXISTS user_guild_channels;
DROP TABLE IF EXISTS guilds;
