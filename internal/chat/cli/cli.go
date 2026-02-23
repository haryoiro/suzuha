package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/event"
)

// Chat implements chat.Interface for stdin/stdout.
type Chat struct {
	in  io.Reader
	out io.Writer
	bus *event.Bus
}

// New creates a CLI chat instance.
func New(in io.Reader, out io.Writer, bus *event.Bus) *Chat {
	return &Chat{in: in, out: out, bus: bus}
}

// Run reads lines from stdin and publishes them as events.
func (c *Chat) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(c.in)
	lines := make(chan string)

	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			if line == "" {
				continue
			}
			c.bus.Publish(event.Event{
				ID:        uuid.NewString(),
				Source:    "cli",
				Type:      "message",
				Payload: map[string]any{
					"content":   line,
					"channel":   "cli",
					"user_id":   "local",
					"user_name": "user",
				},
				Timestamp: time.Now(),
			})
		}
	}
}

// Send writes a message to stdout.
func (c *Chat) Send(_ context.Context, _ string, text string) error {
	_, err := fmt.Fprintln(c.out, text)
	return err
}
