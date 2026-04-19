package location

import (
	"context"
	"database/sql"
	"fmt"
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

// Store manages location data with DB persistence and in-memory cache.
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

// UserLocation combines a location with its device mapping and matched place.
type UserLocation struct {
	Location  *Location      `json:"location"`
	Device    *DeviceMapping `json:"device"`
	PlaceName string         `json:"place_name,omitempty"`
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

// DeviceMappingFor returns the mapping for a device, or nil if not found.
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
