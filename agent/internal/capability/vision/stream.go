package vision

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

)

// DetectionEvent is sent to SSE subscribers.
type DetectionEvent struct {
	Detections  []Detection `json:"detections"`
	InferenceMs float64            `json:"inference_ms"`
	Timestamp   int64              `json:"timestamp"`
	FrameWidth  int                `json:"frame_width"`
	FrameHeight int                `json:"frame_height"`
}

// FrameStore holds the latest camera frame and detection results,
// and manages SSE subscribers for real-time streaming.
type FrameStore struct {
	mu          sync.RWMutex
	frame       []byte
	detections  []Detection
	inferenceMs float64
	updatedAt   time.Time
	frameWidth  int
	frameHeight int

	subMu       sync.Mutex
	subscribers map[int]chan DetectionEvent
	nextID      int
}

// NewFrameStore creates a new FrameStore.
func NewFrameStore() *FrameStore {
	return &FrameStore{
		subscribers: make(map[int]chan DetectionEvent),
	}
}

// UpdateFrame stores the latest JPEG frame.
func (fs *FrameStore) UpdateFrame(jpeg []byte) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.frame = jpeg
	fs.updatedAt = time.Now()
}

// UpdateDetections stores detection results and notifies subscribers.
func (fs *FrameStore) UpdateDetections(result *DetectionResult, width, height int) {
	fs.mu.Lock()
	fs.detections = result.Detections
	fs.inferenceMs = result.InferenceMs
	fs.frameWidth = width
	fs.frameHeight = height
	fs.mu.Unlock()

	evt := DetectionEvent{
		Detections:  result.Detections,
		InferenceMs: result.InferenceMs,
		Timestamp:   time.Now().UnixMilli(),
		FrameWidth:  width,
		FrameHeight: height,
	}

	fs.subMu.Lock()
	for _, ch := range fs.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
	fs.subMu.Unlock()
}

// subscribe adds a new SSE subscriber and returns its channel and unsubscribe function.
func (fs *FrameStore) subscribe() (<-chan DetectionEvent, func()) {
	ch := make(chan DetectionEvent, 8)
	fs.subMu.Lock()
	id := fs.nextID
	fs.nextID++
	fs.subscribers[id] = ch
	fs.subMu.Unlock()

	return ch, func() {
		fs.subMu.Lock()
		delete(fs.subscribers, id)
		fs.subMu.Unlock()
	}
}

// LatestFrame returns the latest JPEG frame, or nil if no frame is available.
func (fs *FrameStore) LatestFrame() []byte {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.frame
}

// WaitForNewFrame waits for a new frame to arrive (after calling this method).
// Returns the new frame or nil if timeout.
func (fs *FrameStore) WaitForNewFrame(timeout time.Duration) []byte {
	ch := make(chan []byte, 1)

	fs.mu.RLock()
	oldUpdatedAt := fs.updatedAt
	fs.mu.RUnlock()

	go func() {
		deadline := time.After(timeout)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				ch <- nil
				return
			case <-ticker.C:
				fs.mu.RLock()
				if fs.updatedAt.After(oldUpdatedAt) && fs.frame != nil {
					frame := fs.frame
					fs.mu.RUnlock()
					ch <- frame
					return
				}
				fs.mu.RUnlock()
			}
		}
	}()

	return <-ch
}

// FrameHandler returns an HTTP handler that serves the latest JPEG frame.
func (fs *FrameStore) FrameHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fs.mu.RLock()
		frame := fs.frame
		fs.mu.RUnlock()

		if frame == nil {
			http.Error(w, "no frame available", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(frame)
	}
}

// DetectionStreamHandler returns an SSE handler that streams detection results.
func (fs *FrameStore) DetectionStreamHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch, unsub := fs.subscribe()
		defer unsub()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case evt := <-ch:
				data, err := json.Marshal(evt)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}
