-- +goose Up

-- Provider connections (credentials + endpoint).
CREATE TABLE IF NOT EXISTS llm_providers (
    name     TEXT PRIMARY KEY,
    type     TEXT NOT NULL,                          -- "openai", "zhipu", "gemini", "qwen"
    api_key  TEXT NOT NULL DEFAULT '',               -- AES-256-GCM encrypted
    api_base TEXT NOT NULL DEFAULT '',
    source   TEXT NOT NULL DEFAULT 'seed',           -- "seed" or "user"
    created_at DATETIME DEFAULT (datetime('now')),
    updated_at DATETIME DEFAULT (datetime('now'))
);

-- Model catalog: capabilities and context window per model.
CREATE TABLE IF NOT EXISTS llm_model_catalog (
    provider_name TEXT NOT NULL REFERENCES llm_providers(name) ON DELETE CASCADE,
    model_id      TEXT NOT NULL,
    capabilities  TEXT NOT NULL DEFAULT '["text"]',  -- JSON array
    max_context   INTEGER NOT NULL DEFAULT 0,
    source        TEXT NOT NULL DEFAULT 'static',    -- "static", "api", "user"
    created_at    DATETIME DEFAULT (datetime('now')),
    PRIMARY KEY (provider_name, model_id)
);

-- Extend role assignments with provider+model columns.
-- Old 'preset' column is kept for backward compat migration.
ALTER TABLE llm_role_assignments ADD COLUMN provider_name TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_role_assignments ADD COLUMN model_id TEXT NOT NULL DEFAULT '';

-- +goose Down
DROP TABLE IF EXISTS llm_model_catalog;
DROP TABLE IF EXISTS llm_providers;
