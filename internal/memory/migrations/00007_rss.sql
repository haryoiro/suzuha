-- +goose Up
CREATE TABLE IF NOT EXISTS rss_feeds (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL UNIQUE,
    channel_id   TEXT NOT NULL,
    created_by   TEXT,
    enabled      INTEGER NOT NULL DEFAULT 1,
    last_polled  DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rss_items (
    id           TEXT PRIMARY KEY,
    feed_id      TEXT NOT NULL REFERENCES rss_feeds(id) ON DELETE CASCADE,
    guid         TEXT NOT NULL,
    title        TEXT NOT NULL,
    link         TEXT NOT NULL,
    description  TEXT,
    published_at DATETIME,
    memory_id    TEXT,
    notified     INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(feed_id, guid)
);

CREATE INDEX IF NOT EXISTS idx_rss_items_feed ON rss_items(feed_id);
CREATE INDEX IF NOT EXISTS idx_rss_items_notified ON rss_items(notified) WHERE notified = 0;

-- +goose Down
DROP INDEX IF EXISTS idx_rss_items_notified;
DROP INDEX IF EXISTS idx_rss_items_feed;
DROP TABLE IF EXISTS rss_items;
DROP TABLE IF EXISTS rss_feeds;
