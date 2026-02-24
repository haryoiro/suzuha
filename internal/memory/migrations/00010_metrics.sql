-- +goose Up
CREATE TABLE IF NOT EXISTS metrics (
  name TEXT NOT NULL,
  labels TEXT NOT NULL DEFAULT '{}',
  value REAL NOT NULL DEFAULT 0,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (name, labels)
);

CREATE TABLE IF NOT EXISTS metric_histogram_buckets (
  name TEXT NOT NULL,
  le REAL NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (name, le)
);

-- +goose Down
DROP TABLE IF EXISTS metric_histogram_buckets;
DROP TABLE IF EXISTS metrics;
