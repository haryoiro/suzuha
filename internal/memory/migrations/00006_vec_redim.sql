-- +goose Up
-- Recreate memories_vec with 1024 dimensions and cosine distance.
-- Existing 768-dim embeddings are incompatible and will be re-generated.
DROP TABLE IF EXISTS memories_vec;
CREATE VIRTUAL TABLE IF NOT EXISTS memories_vec USING vec0(
	id TEXT PRIMARY KEY,
	embedding float[1024] distance_metric=cosine
);

-- +goose Down
DROP TABLE IF EXISTS memories_vec;
CREATE VIRTUAL TABLE IF NOT EXISTS memories_vec USING vec0(
	id TEXT PRIMARY KEY,
	embedding float[768]
);
