-- +goose Up
CREATE TABLE IF NOT EXISTS channel_settings (
  channel_id   TEXT PRIMARY KEY,
  guild_id     TEXT NOT NULL DEFAULT '',
  mode         TEXT NOT NULL DEFAULT 'active',
  use_identity BOOLEAN NOT NULL DEFAULT 0,
  updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_cs_guild ON channel_settings(guild_id);

-- +goose Down
DROP INDEX IF EXISTS idx_cs_guild;
DROP TABLE IF EXISTS channel_settings;
