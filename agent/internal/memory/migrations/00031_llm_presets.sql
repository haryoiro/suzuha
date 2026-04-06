-- +goose Up
CREATE TABLE llm_presets (
    name         TEXT PRIMARY KEY,
    provider     TEXT NOT NULL,
    model        TEXT NOT NULL,
    api_key      TEXT NOT NULL DEFAULT '',
    api_base     TEXT NOT NULL DEFAULT '',
    max_tokens   INTEGER NOT NULL DEFAULT 0,
    capabilities TEXT NOT NULL DEFAULT '["text"]',
    source       TEXT NOT NULL DEFAULT 'user',
    created_at   DATETIME DEFAULT (datetime('now')),
    updated_at   DATETIME DEFAULT (datetime('now'))
);

CREATE TABLE llm_role_assignments (
    role   TEXT PRIMARY KEY,
    preset TEXT NOT NULL REFERENCES llm_presets(name) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS llm_role_assignments;
DROP TABLE IF EXISTS llm_presets;
