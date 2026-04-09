package location

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// QueryLatestByUserID queries the DB for the latest location of each device linked to a user ID.
// It also checks if the location matches any configured place (geofence).
func (s *Store) QueryLatestByUserID(ctx context.Context, userID string) ([]UserLocation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			l.id, l.device_id, l.latitude, l.longitude, l.altitude, l.speed,
			l.horizontal_accuracy, l.battery_level, l.battery_state, l.motion, l.wifi, l.address,
			l.timestamp, l.created_at,
			d.owner_name, COALESCE(u.display_name, '') AS display_name
		FROM locations l
		INNER JOIN (
			SELECT device_id, MAX(timestamp) AS max_ts
			FROM locations
			WHERE device_id IN (SELECT device_id FROM location_devices WHERE user_id = $1)
			GROUP BY device_id
		) latest ON l.device_id = latest.device_id AND l.timestamp = latest.max_ts
		INNER JOIN location_devices d ON l.device_id = d.device_id
		LEFT JOIN users u ON d.user_id = u.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("location: ユーザーによるクエリに失敗: %w", err)
	}
	defer rows.Close()

	// Load places for geofence matching.
	places, err := s.ListPlaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("location: 場所の読み込みに失敗: %w", err)
	}

	var out []UserLocation
	for rows.Next() {
		var loc Location
		var altitude, speed, accuracy, batteryLevel sql.NullFloat64
		var batteryState, motion, wifi, address sql.NullString
		var ownerName, displayName string
		if err := rows.Scan(
			&loc.ID, &loc.DeviceID, &loc.Latitude, &loc.Longitude,
			&altitude, &speed, &accuracy,
			&batteryLevel, &batteryState, &motion, &wifi, &address,
			&loc.Timestamp, &loc.CreatedAt,
			&ownerName, &displayName,
		); err != nil {
			return nil, fmt.Errorf("location: スキャンに失敗: %w", err)
		}
		loc.Altitude = altitude.Float64
		loc.Speed = speed.Float64
		loc.HorizontalAccuracy = accuracy.Float64
		loc.BatteryLevel = batteryLevel.Float64
		loc.BatteryState = batteryState.String
		loc.Motion = motion.String
		loc.Wifi = wifi.String
		loc.Address = address.String

		dm := &DeviceMapping{
			DeviceID:        loc.DeviceID,
			OwnerName:       ownerName,
			UserID:          userID,
			UserDisplayName: displayName,
		}

		// Find matching place.
		var placeName string
		for _, p := range places {
			dist := haversineM(loc.Latitude, loc.Longitude, p.Latitude, p.Longitude)
			r := p.RadiusM
			if r <= 0 {
				r = 50
			}
			if dist <= r {
				placeName = p.Name
				break
			}
		}

		out = append(out, UserLocation{
			Location:  &loc,
			Device:    dm,
			PlaceName: placeName,
		})
	}
	return out, rows.Err()
}

// History queries historical locations for a device within a time range.
func (s *Store) History(ctx context.Context, deviceID string, since, until time.Time, limit int) ([]Location, error) {
	query := `
		SELECT id, device_id, latitude, longitude, altitude, speed,
			horizontal_accuracy, battery_level, battery_state, motion, wifi, address, timestamp, created_at
		FROM locations
		WHERE device_id = $1 AND timestamp >= $2 AND timestamp <= $3
		ORDER BY timestamp DESC
		LIMIT $4
	`
	rows, err := s.db.QueryContext(ctx, query, deviceID, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("location: クエリに失敗: %w", err)
	}
	defer rows.Close()

	var locs []Location
	for rows.Next() {
		var loc Location
		var batteryLevel, altitude, speed, accuracy sql.NullFloat64
		var batteryState, motion, wifi, address sql.NullString
		if err := rows.Scan(
			&loc.ID, &loc.DeviceID, &loc.Latitude, &loc.Longitude,
			&altitude, &speed, &accuracy,
			&batteryLevel, &batteryState, &motion, &wifi, &address,
			&loc.Timestamp, &loc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("location: スキャンに失敗: %w", err)
		}
		loc.Altitude = altitude.Float64
		loc.Speed = speed.Float64
		loc.HorizontalAccuracy = accuracy.Float64
		loc.BatteryLevel = batteryLevel.Float64
		loc.BatteryState = batteryState.String
		loc.Motion = motion.String
		loc.Wifi = wifi.String
		loc.Address = address.String
		locs = append(locs, loc)
	}
	return locs, rows.Err()
}

// HistoryAll queries historical locations for all devices within a time range.
func (s *Store) HistoryAll(ctx context.Context, since, until time.Time, limit int) ([]Location, error) {
	query := `
		SELECT id, device_id, latitude, longitude, altitude, speed,
			horizontal_accuracy, battery_level, battery_state, motion, wifi, address, timestamp, created_at
		FROM locations
		WHERE timestamp >= $1 AND timestamp <= $2
		ORDER BY timestamp DESC
		LIMIT $3
	`
	rows, err := s.db.QueryContext(ctx, query, since, until, limit)
	if err != nil {
		return nil, fmt.Errorf("location: クエリに失敗: %w", err)
	}
	defer rows.Close()

	var locs []Location
	for rows.Next() {
		var loc Location
		var batteryLevel, altitude, speed, accuracy sql.NullFloat64
		var batteryState, motion, wifi, address sql.NullString
		if err := rows.Scan(
			&loc.ID, &loc.DeviceID, &loc.Latitude, &loc.Longitude,
			&altitude, &speed, &accuracy,
			&batteryLevel, &batteryState, &motion, &wifi, &address,
			&loc.Timestamp, &loc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("location: スキャンに失敗: %w", err)
		}
		loc.Altitude = altitude.Float64
		loc.Speed = speed.Float64
		loc.HorizontalAccuracy = accuracy.Float64
		loc.BatteryLevel = batteryLevel.Float64
		loc.BatteryState = batteryState.String
		loc.Motion = motion.String
		loc.Wifi = wifi.String
		loc.Address = address.String
		locs = append(locs, loc)
	}
	return locs, rows.Err()
}

// LoadSettings loads devices and places from the database into memory.
// When a device has a user_id, the owner_name is resolved from the users table.
func (s *Store) LoadSettings(ctx context.Context) error {
	devices := make(map[string]*DeviceMapping)
	dRows, err := s.db.QueryContext(ctx, `
		SELECT d.device_id, d.owner_name, d.user_id, COALESCE(u.display_name, '') AS display_name
		FROM location_devices d
		LEFT JOIN users u ON d.user_id = u.id`)
	if err != nil {
		return fmt.Errorf("location: デバイスの読み込みに失敗: %w", err)
	}
	defer dRows.Close()
	for dRows.Next() {
		var dm DeviceMapping
		var userID sql.NullString
		var displayName string
		if err := dRows.Scan(&dm.DeviceID, &dm.OwnerName, &userID, &displayName); err != nil {
			return fmt.Errorf("location: デバイスのスキャンに失敗: %w", err)
		}
		if userID.Valid {
			dm.UserID = userID.String
			// Prefer the live display_name from users table.
			if displayName != "" {
				dm.OwnerName = displayName
			}
		}
		devices[dm.DeviceID] = &dm
	}
	if err := dRows.Err(); err != nil {
		return fmt.Errorf("location: デバイス行の読み取りに失敗: %w", err)
	}

	var places []Place
	pRows, err := s.db.QueryContext(ctx, `SELECT id, name, latitude, longitude, radius_m FROM location_places`)
	if err != nil {
		return fmt.Errorf("location: 場所の読み込みに失敗: %w", err)
	}
	defer pRows.Close()
	for pRows.Next() {
		var p Place
		if err := pRows.Scan(&p.ID, &p.Name, &p.Latitude, &p.Longitude, &p.RadiusM); err != nil {
			return fmt.Errorf("location: 場所のスキャンに失敗: %w", err)
		}
		places = append(places, p)
	}
	if err := pRows.Err(); err != nil {
		return fmt.Errorf("location: 場所行の読み取りに失敗: %w", err)
	}

	s.mu.Lock()
	s.devices = devices
	s.places = places
	// Initialize placeState from current cache.
	for deviceID, loc := range s.cache {
		s.placeState[deviceID] = s.nearestPlace(loc.Latitude, loc.Longitude)
	}
	s.mu.Unlock()
	return nil
}

// ListDevices returns all device mappings.
func (s *Store) ListDevices(ctx context.Context) ([]DeviceMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.device_id, d.owner_name, COALESCE(d.user_id, ''), COALESCE(u.display_name, ''), d.created_at
		FROM location_devices d
		LEFT JOIN users u ON d.user_id = u.id
		ORDER BY d.created_at`)
	if err != nil {
		return nil, fmt.Errorf("location: デバイス一覧の取得に失敗: %w", err)
	}
	defer rows.Close()
	var out []DeviceMapping
	for rows.Next() {
		var d DeviceMapping
		if err := rows.Scan(&d.DeviceID, &d.OwnerName, &d.UserID, &d.UserDisplayName, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("location: デバイスのスキャンに失敗: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpsertDevice creates or updates a device mapping.
// userID may be empty if no user linkage is desired.
func (s *Store) UpsertDevice(ctx context.Context, deviceID, ownerName, userID string) error {
	var uid any
	if userID != "" {
		uid = userID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO location_devices (device_id, owner_name, user_id) VALUES ($1, $2, $3)
		 ON CONFLICT(device_id) DO UPDATE SET owner_name = excluded.owner_name, user_id = excluded.user_id`,
		deviceID, ownerName, uid)
	return err
}

// DeleteDevice removes a device mapping.
func (s *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM location_devices WHERE device_id = $1`, deviceID)
	return err
}

// ListPlaces returns all named places.
func (s *Store) ListPlaces(ctx context.Context) ([]Place, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, latitude, longitude, radius_m, created_at FROM location_places ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("location: 場所一覧の取得に失敗: %w", err)
	}
	defer rows.Close()
	var out []Place
	for rows.Next() {
		var p Place
		if err := rows.Scan(&p.ID, &p.Name, &p.Latitude, &p.Longitude, &p.RadiusM, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("location: 場所のスキャンに失敗: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreatePlace inserts a new place.
func (s *Store) CreatePlace(ctx context.Context, p Place) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO location_places (id, name, latitude, longitude, radius_m) VALUES ($1, $2, $3, $4, $5)`,
		p.ID, p.Name, p.Latitude, p.Longitude, p.RadiusM)
	return err
}

// UpdatePlace updates an existing place.
func (s *Store) UpdatePlace(ctx context.Context, p Place) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE location_places SET name = $1, latitude = $2, longitude = $3, radius_m = $4 WHERE id = $5`,
		p.Name, p.Latitude, p.Longitude, p.RadiusM, p.ID)
	return err
}

// DeletePlace removes a place.
func (s *Store) DeletePlace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM location_places WHERE id = $1`, id)
	return err
}

// LoadCache warms the in-memory cache from the database on startup.
func (s *Store) LoadCache(ctx context.Context) error {
	if err := s.LoadSettings(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.device_id, l.latitude, l.longitude, l.altitude, l.speed,
			l.horizontal_accuracy, l.battery_level, l.battery_state, l.motion, l.wifi, l.address,
			l.timestamp, l.created_at
		FROM locations l
		INNER JOIN (
			SELECT device_id, MAX(timestamp) AS max_ts
			FROM locations
			GROUP BY device_id
		) latest ON l.device_id = latest.device_id AND l.timestamp = latest.max_ts
	`)
	if err != nil {
		return fmt.Errorf("location: キャッシュの読み込みに失敗: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var loc Location
		var batteryLevel, altitude, speed, accuracy sql.NullFloat64
		var batteryState, motion, wifi, address sql.NullString
		if err := rows.Scan(
			&loc.ID, &loc.DeviceID, &loc.Latitude, &loc.Longitude,
			&altitude, &speed, &accuracy,
			&batteryLevel, &batteryState, &motion, &wifi, &address,
			&loc.Timestamp, &loc.CreatedAt,
		); err != nil {
			return fmt.Errorf("location: キャッシュのスキャンに失敗: %w", err)
		}
		loc.Altitude = altitude.Float64
		loc.Speed = speed.Float64
		loc.HorizontalAccuracy = accuracy.Float64
		loc.BatteryLevel = batteryLevel.Float64
		loc.BatteryState = batteryState.String
		loc.Motion = motion.String
		loc.Wifi = wifi.String
		loc.Address = address.String
		s.cache[loc.DeviceID] = &loc
	}
	return rows.Err()
}
