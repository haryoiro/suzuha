-- +goose Up
CREATE TABLE diary_entries (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    period_start DATETIME NOT NULL,
    period_end DATETIME NOT NULL,
    metadata TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_diary_kind_period ON diary_entries(kind, period_start);

-- 既存の hourly_digest/daily_diary を memories から移行
INSERT INTO diary_entries (id, kind, content, period_start, period_end, metadata, created_at)
SELECT
    m.id,
    json_extract(m.metadata, '$.kind'),
    m.content,
    CASE
        WHEN json_extract(m.metadata, '$.kind') = 'hourly_digest'
            THEN json_extract(m.metadata, '$.hour')
        WHEN json_extract(m.metadata, '$.kind') = 'daily_diary'
            THEN json_extract(m.metadata, '$.date')
        ELSE m.created_at
    END,
    CASE
        WHEN json_extract(m.metadata, '$.kind') = 'hourly_digest'
            THEN datetime(json_extract(m.metadata, '$.hour'), '+1 hour')
        WHEN json_extract(m.metadata, '$.kind') = 'daily_diary'
            THEN datetime(json_extract(m.metadata, '$.date'), '+1 day')
        ELSE m.created_at
    END,
    m.metadata,
    m.created_at
FROM memories m
WHERE m.type = 'self'
  AND json_extract(m.metadata, '$.kind') IN ('hourly_digest', 'daily_diary');

-- 移行済みエントリを memories から削除
DELETE FROM memories
WHERE type = 'self'
  AND json_extract(metadata, '$.kind') IN ('hourly_digest', 'daily_diary');

-- 対応する FTS と vec エントリも削除（orphan 防止）
DELETE FROM memories_fts WHERE rowid NOT IN (SELECT rowid FROM memories);

-- +goose Down
-- memories に戻す（簡易: content と metadata のみ）
INSERT INTO memories (id, type, content, metadata, created_at, updated_at)
SELECT id, 'self', content, metadata, created_at, created_at
FROM diary_entries;

DROP INDEX IF EXISTS idx_diary_kind_period;
DROP TABLE IF EXISTS diary_entries;
