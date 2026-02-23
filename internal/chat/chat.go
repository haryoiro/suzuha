package chat

import "context"

// Interface is the abstraction for chat platforms (Discord, CLI, etc.).
// Each implementation runs its own event loop, converts platform messages
// to events, and sends responses back.
type Interface interface {
	// Run starts the chat platform event loop. It blocks until ctx is canceled.
	Run(ctx context.Context) error

	// Send sends a message to the specified channel.
	Send(ctx context.Context, channel string, text string) error
}
