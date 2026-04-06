-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_apps (
    name         TEXT PRIMARY KEY,
    title        TEXT,
    description  TEXT,
    version      TEXT,
    registry_type TEXT NOT NULL,
    identifier   TEXT NOT NULL,
    command      TEXT NOT NULL,
    args         TEXT NOT NULL DEFAULT '[]',
    env          TEXT NOT NULL DEFAULT '{}',
    transport    TEXT NOT NULL DEFAULT 'stdio',
    installed_at DATETIME NOT NULL DEFAULT (datetime('now')),
    enabled      INTEGER NOT NULL DEFAULT 1
);

-- +goose Down
DROP TABLE IF EXISTS mcp_apps;
