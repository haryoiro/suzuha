package location

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Location represents a single GPS location record.
type Location struct {
	ID                 string
	DeviceID           string
	Latitude           float64
	Longitude          float64
	Altitude           float64
	Speed              float64
	HorizontalAccuracy float64
	BatteryLevel       float64
	BatteryState       string
	Motion             string // JSON array as string, e.g. ["driving"]
	Wifi               string
	Address            string
	Timestamp          time.Time
	CreatedAt          time.Time
}

// OverlandPayload matches the Overland app's POST body format.
type OverlandPayload struct {
	Locations []GeoJSONFeature `json:"locations"`
	Current   *GeoJSONFeature  `json:"current,omitempty"`
}

// GeoJSONFeature is a GeoJSON Feature object.
type GeoJSONFeature struct {
	Type       string          `json:"type"`
	Geometry   GeoJSONGeometry `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

// GeoJSONGeometry is a GeoJSON geometry with coordinates.
type GeoJSONGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"` // [longitude, latitude]
}

// geofenceEvent records a device entering or leaving a named place.
type geofenceEvent struct {
	DeviceID  string
	PlaceName string
	EventType string // "arrived" or "left"
	Timestamp time.Time
}

// Store manages location data with SQLite persistence and in-memory cache.
type Store struct {
	db         *sql.DB
	mu         sync.RWMutex
	cache      map[string]*Location       // device_id -> latest location
	devices    map[string]*DeviceMapping   // device_id -> mapping (with user_id)
	places     []Place                     // named places
	placeState map[string]string           // device_id -> current place name ("" = nowhere)
	geoEvents  []geofenceEvent             // recent geofence events (kept for 1 hour)
}

// DeviceMapping maps a device ID to its owner (user).
type DeviceMapping struct {
	DeviceID        string `json:"device_id"`
	OwnerName       string `json:"owner_name"`
	UserID          string `json:"user_id,omitempty"`
	UserDisplayName string `json:"user_display_name,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
}

// Place is a named geographic location (e.g. home, office).
type Place struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	RadiusM   float64 `json:"radius_m"`
	CreatedAt string  `json:"created_at,omitempty"`
}

// NewStore creates a location Store.
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:         db,
		cache:      make(map[string]*Location),
		devices:    make(map[string]*DeviceMapping),
		placeState: make(map[string]string),
	}
}

// OwnerName returns the configured owner name for a device, or the device_id itself.
func (s *Store) OwnerName(deviceID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ownerName(deviceID)
}

// ownerName is the lock-free internal version of OwnerName.
// Caller must hold s.mu (read or write).
func (s *Store) ownerName(deviceID string) string {
	if dm, ok := s.devices[deviceID]; ok {
		return dm.OwnerName
	}
	return deviceID
}

// DeviceMapping returns the mapping for a device, or nil if not found.
func (s *Store) DeviceMappingFor(deviceID string) *DeviceMapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.devices[deviceID]
}

// NearestPlace returns the name of the nearest configured place if within its radius.
func (s *Store) NearestPlace(lat, lon float64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nearestPlace(lat, lon)
}

// nearestPlace is the lock-free internal version of NearestPlace.
// Caller must hold s.mu (read or write).
func (s *Store) nearestPlace(lat, lon float64) string {
	for _, p := range s.places {
		dist := haversineM(lat, lon, p.Latitude, p.Longitude)
		r := p.RadiusM
		if r <= 0 {
			r = 50 // default 50m
		}
		if dist <= r {
			return p.Name
		}
	}
	return ""
}

// Setup creates the locations table if it doesn't exist (fallback for non-goose setups).
func (s *Store) Setup(ctx context.Context) error {
	// Table is created by goose migration 00020_locations.sql.
	// This is a no-op safety net.
	return nil
}

// SaveBatch inserts a batch of locations and updates the in-memory cache.
func (s *Store) SaveBatch(ctx context.Context, locs []Location) error {
	if len(locs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("location: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO locations (id, device_id, latitude, longitude, altitude, speed,
			horizontal_accuracy, battery_level, battery_state, motion, wifi, address, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("location: ステートメント準備に失敗: %w", err)
	}
	defer stmt.Close()

	for i := range locs {
		loc := &locs[i]
		if loc.ID == "" {
			loc.ID = uuid.NewString()
		}
		_, err := stmt.ExecContext(ctx,
			loc.ID, loc.DeviceID, loc.Latitude, loc.Longitude, loc.Altitude, loc.Speed,
			loc.HorizontalAccuracy, loc.BatteryLevel, loc.BatteryState, loc.Motion,
			loc.Wifi, loc.Address, loc.Timestamp,
		)
		if err != nil {
			return fmt.Errorf("location: 挿入に失敗: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("location: コミットに失敗: %w", err)
	}

	// Update cache with the latest location per device and detect geofence transitions.
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range locs {
		loc := &locs[i]
		existing, ok := s.cache[loc.DeviceID]
		if !ok || loc.Timestamp.After(existing.Timestamp) {
			clone := *loc
			s.cache[loc.DeviceID] = &clone

			// Geofence detection.
			prevPlace := s.placeState[loc.DeviceID]
			newPlace := s.nearestPlace(loc.Latitude, loc.Longitude)
			if newPlace != prevPlace {
				if prevPlace != "" {
					s.geoEvents = append(s.geoEvents, geofenceEvent{
						DeviceID:  loc.DeviceID,
						PlaceName: prevPlace,
						EventType: "left",
						Timestamp: loc.Timestamp,
					})
				}
				if newPlace != "" {
					s.geoEvents = append(s.geoEvents, geofenceEvent{
						DeviceID:  loc.DeviceID,
						PlaceName: newPlace,
						EventType: "arrived",
						Timestamp: loc.Timestamp,
					})
				}
				s.placeState[loc.DeviceID] = newPlace
			}
		}
	}

	// Prune events older than 1 hour.
	cutoff := time.Now().Add(-1 * time.Hour)
	n := 0
	for _, e := range s.geoEvents {
		if e.Timestamp.After(cutoff) {
			s.geoEvents[n] = e
			n++
		}
	}
	s.geoEvents = s.geoEvents[:n]

	return nil
}

// Latest returns the most recent cached location for a device. Returns nil if unknown.
func (s *Store) Latest(deviceID string) *Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[deviceID]
}

// UserLocation combines a location with its device mapping and matched place.
type UserLocation struct {
	Location  *Location      `json:"location"`
	Device    *DeviceMapping `json:"device"`
	PlaceName string         `json:"place_name,omitempty"`
}

// LatestByUserID returns the most recent cached locations for all devices linked to a user ID.
// Each result includes the matched place name if the location falls within a geofence.
func (s *Store) LatestByUserID(userID string) []UserLocation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []UserLocation
	for deviceID, dm := range s.devices {
		if dm.UserID != userID {
			continue
		}
		loc, ok := s.cache[deviceID]
		if !ok {
			continue
		}
		ul := UserLocation{
			Location:  loc,
			Device:    dm,
			PlaceName: s.nearestPlace(loc.Latitude, loc.Longitude),
		}
		out = append(out, ul)
	}
	return out
}

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
			WHERE device_id IN (SELECT device_id FROM location_devices WHERE user_id = ?)
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

// LatestAll returns the most recent cached location for every known device.
func (s *Store) LatestAll() []*Location {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Location, 0, len(s.cache))
	for _, loc := range s.cache {
		out = append(out, loc)
	}
	return out
}

// History queries historical locations for a device within a time range.
func (s *Store) History(ctx context.Context, deviceID string, since, until time.Time, limit int) ([]Location, error) {
	query := `
		SELECT id, device_id, latitude, longitude, altitude, speed,
			horizontal_accuracy, battery_level, battery_state, motion, wifi, address, timestamp, created_at
		FROM locations
		WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
		LIMIT ?
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
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp DESC
		LIMIT ?
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

// --- Device CRUD ---

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
		`INSERT INTO location_devices (device_id, owner_name, user_id) VALUES (?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET owner_name = excluded.owner_name, user_id = excluded.user_id`,
		deviceID, ownerName, uid)
	return err
}

// DeleteDevice removes a device mapping.
func (s *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM location_devices WHERE device_id = ?`, deviceID)
	return err
}

// --- Place CRUD ---

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
		`INSERT INTO location_places (id, name, latitude, longitude, radius_m) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Latitude, p.Longitude, p.RadiusM)
	return err
}

// UpdatePlace updates an existing place.
func (s *Store) UpdatePlace(ctx context.Context, p Place) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE location_places SET name = ?, latitude = ?, longitude = ?, radius_m = ? WHERE id = ?`,
		p.Name, p.Latitude, p.Longitude, p.RadiusM, p.ID)
	return err
}

// DeletePlace removes a place.
func (s *Store) DeletePlace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM location_places WHERE id = ?`, id)
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

// ParseOverlandPayload converts an OverlandPayload into a slice of Location.
func ParseOverlandPayload(payload *OverlandPayload) []Location {
	var locs []Location
	for _, feat := range payload.Locations {
		if loc, ok := featureToLocation(&feat); ok {
			locs = append(locs, loc)
		}
	}
	if payload.Current != nil {
		if loc, ok := featureToLocation(payload.Current); ok {
			locs = append(locs, loc)
		}
	}
	return locs
}

func featureToLocation(feat *GeoJSONFeature) (Location, bool) {
	if len(feat.Geometry.Coordinates) < 2 {
		return Location{}, false
	}

	props := feat.Properties
	loc := Location{
		Longitude: feat.Geometry.Coordinates[0],
		Latitude:  feat.Geometry.Coordinates[1],
	}

	if v, ok := props["device_id"].(string); ok {
		loc.DeviceID = v
	}
	if loc.DeviceID == "" {
		loc.DeviceID = "default"
	}

	if v, ok := props["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			loc.Timestamp = t
		}
	}
	if loc.Timestamp.IsZero() {
		loc.Timestamp = time.Now()
	}

	if v, ok := props["altitude"].(float64); ok {
		loc.Altitude = v
	}
	if v, ok := props["speed"].(float64); ok {
		loc.Speed = v
	}
	if v, ok := props["horizontal_accuracy"].(float64); ok {
		loc.HorizontalAccuracy = v
	}
	if v, ok := props["battery_level"].(float64); ok {
		loc.BatteryLevel = v
	}
	if v, ok := props["battery_state"].(string); ok {
		loc.BatteryState = v
	}
	if v, ok := props["wifi"].(string); ok {
		loc.Wifi = v
	}

	// Motion is an array in Overland; store as JSON string.
	if v, ok := props["motion"]; ok {
		if b, err := json.Marshal(v); err == nil {
			loc.Motion = string(b)
		}
	}

	return loc, true
}

// haversineM returns the distance in meters between two lat/lon points.
func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthR = 6_371_000 // meters
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthR * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
