-- +goose Up
CREATE TABLE IF NOT EXISTS locations (
    id         TEXT PRIMARY KEY,
    device_id  TEXT NOT NULL,
    latitude   REAL NOT NULL,
    longitude  REAL NOT NULL,
    altitude   REAL NOT NULL DEFAULT 0,
    speed      REAL NOT NULL DEFAULT 0,
    horizontal_accuracy REAL NOT NULL DEFAULT 0,
    battery_level  REAL,
    battery_state  TEXT,
    motion     TEXT,
    wifi       TEXT,
    address    TEXT,
    timestamp  DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_locations_device_ts ON locations(device_id, timestamp DESC);
CREATE INDEX idx_locations_ts ON locations(timestamp DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_locations_ts;
DROP INDEX IF EXISTS idx_locations_device_ts;
DROP TABLE IF EXISTS locations;
