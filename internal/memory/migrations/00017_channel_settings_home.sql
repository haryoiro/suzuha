-- +goose Up
ALTER TABLE channel_settings RENAME COLUMN use_identity TO home;

-- +goose Down
ALTER TABLE channel_settings RENAME COLUMN home TO use_identity;
