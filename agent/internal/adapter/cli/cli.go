package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/haryoiro/suzuha/internal/event"
)

// Chat は stdin/stdout を使う CLI チャットアダプタ。
// chat.Interface と gateway.Source を満たす。
type Chat struct {
	in  io.Reader
	out io.Writer
	bus *event.Bus
}

// New creates a CLI chat instance.
func New(in io.Reader, out io.Writer, bus *event.Bus) *Chat {
	return &Chat{in: in, out: out, bus: bus}
}

// Name は gateway.Source を満たす。
func (c *Chat) Name() string { return "cli" }

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
			c.bus.Publish(event.NewMessageEvent("cli", event.MessagePayload{
				Content:  line,
				Channel:  "cli",
				UserID:   "local",
				UserName: "user",
			}))
		}
	}
}

// Send writes a message to stdout.
func (c *Chat) Send(_ context.Context, _ string, text string) error {
	_, err := fmt.Fprintln(c.out, text)
	if err != nil {
		return fmt.Errorf("cli: メッセージの送信に失敗: %w", err)
	}
	return nil
}
