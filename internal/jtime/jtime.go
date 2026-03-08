// Package jtime provides timezone-aware time utilities.
// Call Init once at startup with the configured location.
// All other functions use the configured location.
package jtime

import (
	"time"
)

var loc = time.UTC

// Init sets the global timezone location.
// Must be called once at startup before other functions.
func Init(l *time.Location) {
	if l != nil {
		loc = l
	}
}

// Now returns the current time in the configured timezone.
func Now() time.Time {
	return time.Now().In(loc)
}

// Location returns the configured timezone location.
func Location() *time.Location {
	return loc
}

// In converts t to the configured timezone.
func In(t time.Time) time.Time {
	return t.In(loc)
}
