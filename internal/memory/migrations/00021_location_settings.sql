-- +goose Up
CREATE TABLE IF NOT EXISTS location_devices (
    device_id TEXT PRIMARY KEY,
    owner_name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS location_places (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    radius_m REAL NOT NULL DEFAULT 200,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS location_places;
DROP TABLE IF EXISTS location_devices;
