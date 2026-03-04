package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// LogHandler proxies SSE log streams from agent (and optionally other sources).
type LogHandler struct {
	agentURL  string
	consolURL string
	logger    *slog.Logger
}

// NewLogHandler creates a new LogHandler.
func NewLogHandler(agentURL, consolURL string, logger *slog.Logger) *LogHandler {
	return &LogHandler{
		agentURL:  agentURL,
		consolURL: consolURL,
		logger:    logger,
	}
}

type logEntry struct {
	Seq     uint64         `json:"seq"`
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Source  string         `json:"source"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Stream handles GET /api/logs/stream as an SSE endpoint.
// It fans-in log streams from agent and consolidator.
func (h *LogHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	levelFilter := strings.ToUpper(r.URL.Query().Get("level"))
	sourceFilter := r.URL.Query().Get("source")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	entries := make(chan logEntry, 128)
	ctx := r.Context()

	// Start upstream SSE connections.
	if h.agentURL != "" && (sourceFilter == "" || sourceFilter == "agent") {
		go h.proxySSE(ctx, h.agentURL, "agent", entries)
	}
	if h.consolURL != "" && (sourceFilter == "" || sourceFilter == "consolidator") {
		go h.proxySSE(ctx, h.consolURL, "consolidator", entries)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-entries:
			if levelFilter != "" && !strings.EqualFold(entry.Level, levelFilter) {
				continue
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// proxySSE connects to an upstream SSE endpoint and forwards entries.
func (h *LogHandler) proxySSE(ctx context.Context, url, source string, out chan<- logEntry) {
	client := &http.Client{Timeout: 0} // no timeout for SSE
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			h.logger.Error("log proxy request", "source", source, "error", err)
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			h.logger.Warn("log proxy connect", "source", source, "error", err)
			// Retry after a delay.
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var entry logEntry
			if err := json.Unmarshal([]byte(data), &entry); err != nil {
				continue
			}
			entry.Source = source
			select {
			case out <- entry:
			case <-ctx.Done():
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()

		// Connection closed; retry.
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
