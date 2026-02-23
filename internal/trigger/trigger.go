// Package trigger provides scheduled event triggering via cron expressions.
package trigger

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/event"
)

// Trigger fires events on a schedule or condition.
type Trigger interface {
	// Start begins the trigger. It blocks until ctx is canceled.
	Start(ctx context.Context) error
}

// CronTrigger fires events at a fixed interval.
type CronTrigger struct {
	name     string
	interval time.Duration
	payload  map[string]any
	bus      *event.Bus
	logger   *slog.Logger
}

// NewCron creates a periodic trigger.
func NewCron(name string, interval time.Duration, payload map[string]any, bus *event.Bus, logger *slog.Logger) *CronTrigger {
	return &CronTrigger{
		name:     name,
		interval: interval,
		payload:  payload,
		bus:      bus,
		logger:   logger,
	}
}

func (c *CronTrigger) Start(ctx context.Context) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.logger.Debug("trigger fired", "name", c.name)
			c.bus.Publish(event.Event{
				ID:        uuid.NewString(),
				Source:    "trigger",
				Type:      "trigger",
				Payload:   c.payload,
				Timestamp: time.Now(),
			})
		}
	}
}

// Manager runs multiple triggers concurrently.
type Manager struct {
	triggers []Trigger
	logger   *slog.Logger
}

// NewManager creates a trigger manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Add registers a trigger.
func (m *Manager) Add(t Trigger) {
	m.triggers = append(m.triggers, t)
}

// Run starts all triggers. Returns when ctx is canceled.
func (m *Manager) Run(ctx context.Context) error {
	errs := make(chan error, len(m.triggers))

	for _, t := range m.triggers {
		go func(t Trigger) {
			errs <- t.Start(ctx)
		}(t)
	}

	// Wait for ctx cancellation or first error.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errs:
		return err
	}
}
