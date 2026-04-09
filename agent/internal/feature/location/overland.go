package location

import (
	"encoding/json"
	"math"
	"time"
)

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
