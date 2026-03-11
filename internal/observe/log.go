// Package observe provides logging, metrics, and observability utilities.
package observe

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// parseLevel converts a string log level to slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger creates a structured JSON logger with the given level.
func NewLogger(level string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler)
}

// NewLoggerWithRing creates a logger that writes to both stdout (JSON) and a ring buffer.
func NewLoggerWithRing(level string, ring *RingBuffer) *slog.Logger {
	l := parseLevel(level)
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})
	ringHandler := &RingHandler{inner: jsonHandler, ring: ring}
	return slog.New(ringHandler)
}

// RingHandler wraps a slog.Handler and also pushes entries into a RingBuffer.
type RingHandler struct {
	inner slog.Handler
	ring  *RingBuffer
	attrs []slog.Attr
	group string
}

func (h *RingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *RingHandler) Handle(ctx context.Context, record slog.Record) error {
	// Push to ring buffer.
	entry := LogEntry{
		Time:    record.Time,
		Level:   record.Level.String(),
		Message: record.Message,
	}
	if record.NumAttrs() > 0 || len(h.attrs) > 0 {
		attrs := make(map[string]any)
		for _, a := range h.attrs {
			attrs[a.Key] = resolveAttrValue(a)
		}
		record.Attrs(func(a slog.Attr) bool {
			key := a.Key
			if h.group != "" {
				key = h.group + "." + key
			}
			attrs[key] = resolveAttrValue(a)
			return true
		})
		entry.Attrs = attrs
	}
	h.ring.Push(entry)

	// Forward to inner handler (stdout).
	return h.inner.Handle(ctx, record)
}

// resolveAttrValue extracts a JSON-safe value from a slog.Attr.
// error values are converted to their string representation to avoid
// serializing as "{}" (unexported struct fields).
func resolveAttrValue(a slog.Attr) any {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindAny {
		if err, ok := v.Any().(error); ok {
			return err.Error()
		}
	}
	return v.Any()
}

func (h *RingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RingHandler{
		inner: h.inner.WithAttrs(attrs),
		ring:  h.ring,
		attrs: append(h.attrs, attrs...),
		group: h.group,
	}
}

func (h *RingHandler) WithGroup(name string) slog.Handler {
	return &RingHandler{
		inner: h.inner.WithGroup(name),
		ring:  h.ring,
		attrs: h.attrs,
		group: name,
	}
}
