-- +goose Up
-- Recreate memories_vec with 1536 dimensions (Gemini Embedding, Matryoshka).
-- Existing 1024-dim embeddings are incompatible; the background worker
-- will re-embed all memories automatically.
DROP TABLE IF EXISTS memories_vec;
CREATE VIRTUAL TABLE IF NOT EXISTS memories_vec USING vec0(
    id TEXT PRIMARY KEY,
    embedding float[1536] distance_metric=cosine
);

-- +goose Down
DROP TABLE IF EXISTS memories_vec;
CREATE VIRTUAL TABLE IF NOT EXISTS memories_vec USING vec0(
    id TEXT PRIMARY KEY,
    embedding float[1024] distance_metric=cosine
);
