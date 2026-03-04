package location

import (
	"fmt"
	"strings"
	"time"
)

// BuildContextSnippet returns a human-readable summary of all tracked
// devices' latest locations, suitable for injection as an ephemeral system message.
func (s *Store) BuildContextSnippet() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.cache) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, loc := range s.cache {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		s.formatLocation(&sb, loc)
	}

	// Append recent geofence events.
	if len(s.geoEvents) > 0 {
		sb.WriteString("\n---\n")
		for _, e := range s.geoEvents {
			owner := s.ownerName(e.DeviceID)
			age := time.Since(e.Timestamp).Round(time.Minute)
			if e.EventType == "arrived" {
				fmt.Fprintf(&sb, "%s前: %sが%sに到着\n", age, owner, e.PlaceName)
			} else {
				fmt.Fprintf(&sb, "%s前: %sが%sを出発\n", age, owner, e.PlaceName)
			}
		}
	}

	return sb.String()
}

// formatLocation writes a single location line into the builder.
// Caller must hold s.mu.
func (s *Store) formatLocation(sb *strings.Builder, loc *Location) {
	owner := s.ownerName(loc.DeviceID)
	place := s.nearestPlace(loc.Latitude, loc.Longitude)
	age := time.Since(loc.Timestamp).Round(time.Minute)

	fmt.Fprintf(sb, "%sの現在地: ", owner)
	if place != "" {
		fmt.Fprintf(sb, "%s (", place)
	}
	fmt.Fprintf(sb, "Lat=%.6f Lon=%.6f", loc.Latitude, loc.Longitude)
	if place != "" {
		sb.WriteString(")")
	}
	fmt.Fprintf(sb, " Alt=%.0fm Speed=%.1fm/s", loc.Altitude, loc.Speed)
if loc.BatteryLevel > 0 {
		fmt.Fprintf(sb, " Battery=%.0f%%(%s)", loc.BatteryLevel*100, loc.BatteryState)
	}
	if loc.Motion != "" && loc.Motion != "[]" {
		fmt.Fprintf(sb, " Motion=%s", loc.Motion)
	}
	fmt.Fprintf(sb, " (%s ago)", age)
}
