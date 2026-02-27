-- +goose Up
ALTER TABLE users ADD COLUMN closeness REAL NOT NULL DEFAULT 0.0;
ALTER TABLE users ADD COLUMN trust     REAL NOT NULL DEFAULT 0.0;
ALTER TABLE users ADD COLUMN interest  REAL NOT NULL DEFAULT 0.0;

-- Migrate existing affinity → closeness.
UPDATE users SET closeness = affinity WHERE affinity != 0.0;

-- Add axis to affinity_events.
ALTER TABLE affinity_events ADD COLUMN axis TEXT NOT NULL DEFAULT 'closeness';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; these are best-effort.
ALTER TABLE affinity_events DROP COLUMN axis;
ALTER TABLE users DROP COLUMN closeness;
ALTER TABLE users DROP COLUMN trust;
ALTER TABLE users DROP COLUMN interest;
