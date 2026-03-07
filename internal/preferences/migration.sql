-- Preferences: self-directed values and interests.
CREATE TABLE IF NOT EXISTS preferences (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    category   TEXT    NOT NULL DEFAULT '',
    topic      TEXT    NOT NULL,
    stance     TEXT    NOT NULL DEFAULT 'curious',
    confidence REAL    NOT NULL DEFAULT 0.1,
    reasoning  TEXT    NOT NULL DEFAULT '',
    encounters INTEGER NOT NULL DEFAULT 1,
    shared     INTEGER NOT NULL DEFAULT 0,
    last_evaluated_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_preferences_topic ON preferences(topic);
