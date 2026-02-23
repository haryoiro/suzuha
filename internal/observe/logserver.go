package observe

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// LogHandler returns an HTTP handler that streams log entries from the ring buffer via SSE.
func LogHandler(ring *RingBuffer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Send existing entries first.
		entries, _ := ring.Entries(0)
		for _, e := range entries {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()

		// Subscribe for new entries.
		id, ch := ring.Subscribe()
		defer ring.Unsubscribe(id)

		for {
			select {
			case <-r.Context().Done():
				return
			case entry, ok := <-ch:
				if !ok {
					return
				}
				data, _ := json.Marshal(entry)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	})
}
