package event

import "time"

// Event is the common schema for all events from all sources.
type Event struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"`    // "discord" | "cli" | "timer" | "webhook"
	Type      string         `json:"type"`      // "message" | "heartbeat" | "trigger"
	Payload   map[string]any `json:"payload"`   // source-specific data
	Timestamp time.Time      `json:"timestamp"`
}

// Bus is a simple fan-in event bus. All sources publish events,
// the agent consumes them from a single channel.
type Bus struct {
	ch chan Event
}

func NewBus(bufferSize int) *Bus {
	return &Bus{ch: make(chan Event, bufferSize)}
}

// Publish sends an event to the bus. Non-blocking if buffer has space.
func (b *Bus) Publish(e Event) {
	b.ch <- e
}

// Subscribe returns the read-only channel for consuming events.
func (b *Bus) Subscribe() <-chan Event {
	return b.ch
}
