-- +goose Up

-- ParadeDB 拡張を有効化
CREATE EXTENSION IF NOT EXISTS vector;     -- pgvector (ベクトル検索)
CREATE EXTENSION IF NOT EXISTS pg_trgm;    -- trigram (日本語部分文字列マッチ)
CREATE EXTENSION IF NOT EXISTS pg_search;  -- BM25 全文検索 (Tantivy)

-- memories (pgvector 埋め込み列あり)
CREATE TABLE IF NOT EXISTS memories (
    id         TEXT PRIMARY KEY,
    type       TEXT NOT NULL,
    content    TEXT NOT NULL,
    embedding  vector(1536),
    metadata   JSONB,
    keywords   JSONB,
    topic      TEXT,
    persons    JSONB,
    event_time TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);
CREATE INDEX IF NOT EXISTS idx_memories_topic ON memories(topic);
CREATE INDEX IF NOT EXISTS idx_memories_event_time ON memories(event_time);

-- pgvector HNSW インデックス (cosine)
CREATE INDEX IF NOT EXISTS idx_memories_embedding_hnsw ON memories
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- pg_trgm インデックス (日本語部分文字列マッチ)
CREATE INDEX IF NOT EXISTS idx_memories_content_trgm ON memories
    USING gin (content gin_trgm_ops);

-- pg_search BM25 インデックス (Tantivy)
CREATE INDEX idx_memories_bm25 ON memories
USING bm25 (id, content)
WITH (key_field='id');

-- users
CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'member',
    affinity     REAL NOT NULL DEFAULT 0.0,
    metadata     JSONB,
    is_bot       BOOLEAN NOT NULL DEFAULT false,
    closeness    REAL NOT NULL DEFAULT 0.0,
    trust        REAL NOT NULL DEFAULT 0.0,
    interest     REAL NOT NULL DEFAULT 0.0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_users_is_bot ON users(is_bot);

-- platform_links
CREATE TABLE IF NOT EXISTS platform_links (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform         TEXT NOT NULL,
    platform_user_id TEXT NOT NULL,
    platform_name    TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(platform, platform_user_id)
);
CREATE INDEX IF NOT EXISTS idx_platform_links_user ON platform_links(user_id);
CREATE INDEX IF NOT EXISTS idx_platform_links_lookup ON platform_links(platform, platform_user_id);

-- affinity_events
CREATE TABLE IF NOT EXISTS affinity_events (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delta           REAL NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    axis            TEXT NOT NULL DEFAULT 'closeness',
    interaction_ids TEXT,
    group_start     TIMESTAMPTZ,
    group_end       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_affinity_events_user ON affinity_events(user_id);
CREATE INDEX IF NOT EXISTS idx_affinity_events_time ON affinity_events(created_at);

-- guilds
CREATE TABLE IF NOT EXISTS guilds (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- user_guild_channels
CREATE TABLE IF NOT EXISTS user_guild_channels (
    user_id      TEXT NOT NULL,
    guild_id     TEXT NOT NULL,
    channel_id   TEXT NOT NULL,
    channel_name TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, guild_id, channel_id)
);
CREATE INDEX IF NOT EXISTS idx_ugc_user ON user_guild_channels(user_id);

-- channel_settings
CREATE TABLE IF NOT EXISTS channel_settings (
    channel_id TEXT PRIMARY KEY,
    guild_id   TEXT NOT NULL DEFAULT '',
    mode       TEXT NOT NULL DEFAULT 'active',
    home       BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_cs_guild ON channel_settings(guild_id);

-- channel_activity
CREATE TABLE IF NOT EXISTS channel_activity (
    channel_id            TEXT PRIMARY KEY,
    last_user_message_at  TIMESTAMPTZ NOT NULL
);

-- channel_summaries
CREATE TABLE IF NOT EXISTS channel_summaries (
    channel_id   TEXT PRIMARY KEY,
    channel_name TEXT NOT NULL DEFAULT '',
    guild_id     TEXT NOT NULL DEFAULT '',
    guild_name   TEXT NOT NULL DEFAULT '',
    is_dm        BOOLEAN NOT NULL DEFAULT false,
    user_id      TEXT NOT NULL DEFAULT '',
    summary      TEXT NOT NULL DEFAULT '',
    last_active  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- conversation_logs
CREATE TABLE IF NOT EXISTS conversation_logs (
    id           SERIAL PRIMARY KEY,
    turn_id      TEXT NOT NULL,
    channel_id   TEXT NOT NULL,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    user_id      TEXT,
    user_name    TEXT,
    message_id   TEXT,
    tool_calls   TEXT,
    tool_call_id TEXT,
    source_key   TEXT NOT NULL DEFAULT 'discord',
    timestamp    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_convlogs_channel ON conversation_logs(channel_id);
CREATE INDEX IF NOT EXISTS idx_convlogs_turn ON conversation_logs(turn_id);
CREATE INDEX IF NOT EXISTS idx_convlogs_ts ON conversation_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_convlogs_source ON conversation_logs(source_key);

-- context_snapshot
CREATE TABLE IF NOT EXISTS context_snapshot (
    source_key TEXT PRIMARY KEY,
    messages   TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- task_state
CREATE TABLE IF NOT EXISTS task_state (
    task_name  TEXT PRIMARY KEY,
    state      TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- scheduled_actions
CREATE TABLE IF NOT EXISTS scheduled_actions (
    id              TEXT PRIMARY KEY,
    channel_id      TEXT NOT NULL,
    content         TEXT NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    cron_expr       TEXT,
    random_minutes  INTEGER NOT NULL DEFAULT 0,
    created_by      TEXT,
    mode            TEXT NOT NULL DEFAULT 'direct',
    status          TEXT NOT NULL DEFAULT 'pending',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    executed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_scheduled_actions_due ON scheduled_actions(status, scheduled_at);

-- diary_entries
CREATE TABLE IF NOT EXISTS diary_entries (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    content      TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    metadata     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_diary_kind_period ON diary_entries(kind, period_start);

-- locations
CREATE TABLE IF NOT EXISTS locations (
    id                  TEXT PRIMARY KEY,
    device_id           TEXT NOT NULL,
    latitude            REAL NOT NULL,
    longitude           REAL NOT NULL,
    altitude            REAL NOT NULL DEFAULT 0,
    speed               REAL NOT NULL DEFAULT 0,
    horizontal_accuracy REAL NOT NULL DEFAULT 0,
    battery_level       REAL,
    battery_state       TEXT,
    motion              TEXT,
    wifi                TEXT,
    address             TEXT,
    timestamp           TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_locations_device_ts ON locations(device_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_locations_ts ON locations(timestamp DESC);

-- location_devices
CREATE TABLE IF NOT EXISTS location_devices (
    device_id  TEXT PRIMARY KEY,
    owner_name TEXT NOT NULL,
    user_id    TEXT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_location_devices_user_id ON location_devices(user_id);

-- location_places
CREATE TABLE IF NOT EXISTS location_places (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    latitude   REAL NOT NULL,
    longitude  REAL NOT NULL,
    radius_m   REAL NOT NULL DEFAULT 200,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- app_settings
CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- preferences
CREATE TABLE IF NOT EXISTS preferences (
    id                SERIAL PRIMARY KEY,
    category          TEXT NOT NULL DEFAULT '',
    topic             TEXT NOT NULL,
    stance            TEXT NOT NULL DEFAULT 'curious',
    confidence        REAL NOT NULL DEFAULT 0.1,
    reasoning         TEXT NOT NULL DEFAULT '',
    encounters        INTEGER NOT NULL DEFAULT 1,
    shared            BOOLEAN NOT NULL DEFAULT false,
    last_evaluated_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_preferences_topic ON preferences(topic);

-- mcp_apps
CREATE TABLE IF NOT EXISTS mcp_apps (
    name          TEXT PRIMARY KEY,
    title         TEXT,
    description   TEXT,
    version       TEXT,
    registry_type TEXT NOT NULL,
    identifier    TEXT NOT NULL,
    command       TEXT NOT NULL,
    args          JSONB NOT NULL DEFAULT '[]',
    env           JSONB NOT NULL DEFAULT '{}',
    transport     TEXT NOT NULL DEFAULT 'stdio',
    installed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    enabled       BOOLEAN NOT NULL DEFAULT true
);

-- llm_providers
CREATE TABLE IF NOT EXISTS llm_providers (
    name       TEXT PRIMARY KEY,
    type       TEXT NOT NULL,
    api_key    TEXT NOT NULL DEFAULT '',
    api_base   TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT 'seed',
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- llm_model_catalog
CREATE TABLE IF NOT EXISTS llm_model_catalog (
    provider_name TEXT NOT NULL REFERENCES llm_providers(name) ON DELETE CASCADE,
    model_id      TEXT NOT NULL,
    capabilities  JSONB NOT NULL DEFAULT '["text"]',
    max_context   INTEGER NOT NULL DEFAULT 0,
    source        TEXT NOT NULL DEFAULT 'static',
    created_at    TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (provider_name, model_id)
);

-- llm_role_assignments
CREATE TABLE IF NOT EXISTS llm_role_assignments (
    role          TEXT PRIMARY KEY,
    preset        TEXT NOT NULL,
    provider_name TEXT NOT NULL DEFAULT '',
    model_id      TEXT NOT NULL DEFAULT ''
);

-- rss_feeds
CREATE TABLE IF NOT EXISTS rss_feeds (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL UNIQUE,
    channel_id  TEXT NOT NULL,
    created_by  TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    last_polled TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- rss_items
CREATE TABLE IF NOT EXISTS rss_items (
    id           TEXT PRIMARY KEY,
    feed_id      TEXT NOT NULL REFERENCES rss_feeds(id) ON DELETE CASCADE,
    guid         TEXT NOT NULL,
    title        TEXT NOT NULL,
    link         TEXT NOT NULL,
    description  TEXT,
    published_at TIMESTAMPTZ,
    memory_id    TEXT,
    notified     BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(feed_id, guid)
);
CREATE INDEX IF NOT EXISTS idx_rss_items_feed ON rss_items(feed_id);
CREATE INDEX IF NOT EXISTS idx_rss_items_notified ON rss_items(notified) WHERE notified = false;

-- goose version tracking
CREATE TABLE IF NOT EXISTS goose_db_version (
    id         SERIAL PRIMARY KEY,
    version_id BIGINT NOT NULL,
    is_applied BOOLEAN NOT NULL,
    tstamp     TIMESTAMPTZ DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS rss_items;
DROP TABLE IF EXISTS rss_feeds;
DROP TABLE IF EXISTS llm_role_assignments;
DROP TABLE IF EXISTS llm_model_catalog;
DROP TABLE IF EXISTS llm_providers;
DROP TABLE IF EXISTS mcp_apps;
DROP TABLE IF EXISTS preferences;
DROP TABLE IF EXISTS app_settings;
DROP TABLE IF EXISTS location_places;
DROP TABLE IF EXISTS location_devices;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS diary_entries;
DROP TABLE IF EXISTS scheduled_actions;
DROP TABLE IF EXISTS task_state;
DROP TABLE IF EXISTS context_snapshot;
DROP TABLE IF EXISTS conversation_logs;
DROP TABLE IF EXISTS channel_summaries;
DROP TABLE IF EXISTS channel_activity;
DROP TABLE IF EXISTS channel_settings;
DROP TABLE IF EXISTS user_guild_channels;
DROP TABLE IF EXISTS guilds;
DROP TABLE IF EXISTS affinity_events;
DROP TABLE IF EXISTS platform_links;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS memories;
