-- +goose Up
ALTER TABLE location_devices ADD COLUMN user_id TEXT REFERENCES users(id);
CREATE INDEX idx_location_devices_user_id ON location_devices(user_id);

-- Backfill: match existing owner_name to users.display_name where possible.
UPDATE location_devices SET user_id = (
    SELECT u.id FROM users u WHERE u.display_name = location_devices.owner_name LIMIT 1
) WHERE user_id IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_location_devices_user_id;
ALTER TABLE location_devices DROP COLUMN user_id;
