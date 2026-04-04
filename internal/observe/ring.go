package observe

import (
	"sync"
	"time"
)

// LogEntry is a structured log entry stored in the ring buffer.
type LogEntry struct {
	Seq     uint64         `json:"seq"`
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// RingBuffer is a fixed-size circular buffer of log entries with subscriber support.
type RingBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
	cap     int
	nextSeq uint64

	subMu  sync.Mutex
	subs   map[uint64]chan LogEntry
	nextID uint64
}

// NewRingBuffer creates a ring buffer that holds up to capacity entries.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
		subs:    make(map[uint64]chan LogEntry),
	}
}

// Push adds an entry to the ring buffer and notifies subscribers.
func (r *RingBuffer) Push(entry LogEntry) {
	r.mu.Lock()
	entry.Seq = r.nextSeq
	r.nextSeq++
	r.entries[int(entry.Seq)%r.cap] = entry
	if r.size < r.cap {
		r.size++
	}
	r.mu.Unlock()

	// Notify subscribers (non-blocking).
	r.subMu.Lock()
	for _, ch := range r.subs {
		select {
		case ch <- entry:
		default:
			// Drop if subscriber is slow.
		}
	}
	r.subMu.Unlock()
}

// Entries returns all entries with Seq > afterSeq, and the current latest seq.
func (r *RingBuffer) Entries(afterSeq uint64) ([]LogEntry, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil, 0
	}

	latestSeq := r.nextSeq - 1
	if afterSeq >= r.nextSeq {
		return nil, latestSeq
	}

	// Determine start seq (oldest available).
	oldestSeq := uint64(0)
	if r.nextSeq > uint64(r.cap) {
		oldestSeq = r.nextSeq - uint64(r.cap)
	}
	startSeq := afterSeq + 1
	if startSeq < oldestSeq {
		startSeq = oldestSeq
	}

	result := make([]LogEntry, 0, int(r.nextSeq-startSeq))
	for seq := startSeq; seq < r.nextSeq; seq++ {
		result = append(result, r.entries[int(seq)%r.cap])
	}
	return result, latestSeq
}

// Subscribe returns a channel that receives new log entries.
func (r *RingBuffer) Subscribe() (id uint64, ch <-chan LogEntry) {
	c := make(chan LogEntry, 100)
	r.subMu.Lock()
	id = r.nextID
	r.nextID++
	r.subs[id] = c
	r.subMu.Unlock()
	return id, c
}

// Unsubscribe removes a subscriber.
func (r *RingBuffer) Unsubscribe(id uint64) {
	r.subMu.Lock()
	if ch, ok := r.subs[id]; ok {
		close(ch)
		delete(r.subs, id)
	}
	r.subMu.Unlock()
}
