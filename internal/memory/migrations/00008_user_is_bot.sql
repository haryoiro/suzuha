-- +goose Up
ALTER TABLE users ADD COLUMN is_bot INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_users_is_bot ON users(is_bot);

-- +goose Down
DROP INDEX IF EXISTS idx_users_is_bot;
ALTER TABLE users DROP COLUMN is_bot;
