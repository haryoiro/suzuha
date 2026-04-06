-- +goose Up
-- Preferences: self-directed values and interests.
CREATE TABLE IF NOT EXISTS preferences (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    category   TEXT    NOT NULL DEFAULT '',      -- 音楽, 食べ物, 技術, 思想, etc.
    topic      TEXT    NOT NULL,                 -- 具体的な対象
    stance     TEXT    NOT NULL DEFAULT 'curious', -- liked, disliked, curious, undecided
    confidence REAL    NOT NULL DEFAULT 0.1,     -- 0.0-1.0
    reasoning  TEXT    NOT NULL DEFAULT '',      -- なぜそう思うか
    encounters INTEGER NOT NULL DEFAULT 1,       -- 出会った回数
    shared     INTEGER NOT NULL DEFAULT 0,       -- 共有済みか (0/1)
    last_evaluated_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_preferences_topic ON preferences(topic);
