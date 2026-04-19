package vision

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
)

// ChangeDetector tracks YOLO detection state and publishes events
// to the agent bus when significant changes occur.
type ChangeDetector struct {
	mu             sync.Mutex
	prev           map[string]int // label → count
	lastSent       time.Time
	cooldown       time.Duration // minimum interval between events
	bus            *event.Bus
	defaultChannel string // Discord channel ID for notifications
	enabled        bool
}

// NewChangeDetector creates a ChangeDetector.
// cooldown is the minimum time between agent notifications (to avoid spam).
// defaultChannel is the Discord channel ID where notifications are sent.
func NewChangeDetector(bus *event.Bus, cooldown time.Duration, defaultChannel string) *ChangeDetector {
	return &ChangeDetector{
		prev:           make(map[string]int),
		cooldown:       cooldown,
		bus:            bus,
		defaultChannel: defaultChannel,
		enabled:        false,
	}
}

// SetEnabled toggles change detection on/off.
func (cd *ChangeDetector) SetEnabled(v bool) {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.enabled = v
}

// Enabled returns whether change detection is active.
func (cd *ChangeDetector) Enabled() bool {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	return cd.enabled
}

// Update compares new detections with previous state and publishes
// an event if something changed. Returns true if an event was published.
func (cd *ChangeDetector) Update(detections []Detection) bool {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	if !cd.enabled {
		return false
	}

	curr := summarize(detections)

	appeared, disappeared := diff(cd.prev, curr)

	if len(appeared) == 0 && len(disappeared) == 0 {
		cd.prev = curr
		return false
	}

	if time.Since(cd.lastSent) < cd.cooldown {
		return false
	}

	msg := buildChangeMessage(appeared, disappeared)
	if msg == "" {
		cd.prev = curr
		return false
	}

	cd.prev = curr
	cd.lastSent = time.Now()
	cd.bus.Publish(event.Event{
		ID:     uuid.NewString(),
		Source: event.SourceInternal,
		Type:   event.TypeSelfPrompt,
		Message: event.MessagePayload{
			Content: msg,
			Channel: cd.defaultChannel,
		},
		Timestamp: jtime.Now(),
	})

	return true
}

func summarize(detections []Detection) map[string]int {
	m := make(map[string]int)
	for _, d := range detections {
		if d.Confidence >= 0.4 {
			m[d.Label]++
		}
	}
	return m
}

func diff(prev, curr map[string]int) (appeared, disappeared []string) {
	for label, count := range curr {
		if prev[label] == 0 && count > 0 {
			if count == 1 {
				appeared = append(appeared, label)
			} else {
				appeared = append(appeared, fmt.Sprintf("%s(%d)", label, count))
			}
		}
	}
	for label, count := range prev {
		if curr[label] == 0 && count > 0 {
			disappeared = append(disappeared, label)
		}
	}
	sort.Strings(appeared)
	sort.Strings(disappeared)
	return
}

func buildChangeMessage(appeared, disappeared []string) string {
	var parts []string
	if len(appeared) > 0 {
		parts = append(parts, fmt.Sprintf("[視界] %s が見えた", strings.Join(appeared, ", ")))
	}
	if len(disappeared) > 0 {
		parts = append(parts, fmt.Sprintf("[視界] %s が見えなくなった", strings.Join(disappeared, ", ")))
	}
	return strings.Join(parts, "。")
}
