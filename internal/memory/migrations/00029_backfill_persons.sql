-- +goose Up
-- user 型: metadata.user_id → persons カラムにバックフィル
UPDATE memories SET persons = json_array(json_extract(metadata, '$.user_id'))
WHERE type = 'user' AND persons IS NULL AND json_extract(metadata, '$.user_id') IS NOT NULL;

-- episode 型: metadata.participants → persons カラムにバックフィル
UPDATE memories SET persons = json_extract(metadata, '$.participants')
WHERE type = 'episode' AND persons IS NULL AND json_extract(metadata, '$.participants') IS NOT NULL;

-- +goose Down
UPDATE memories SET persons = NULL WHERE persons IS NOT NULL;
