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

// Replier is an optional interface for platforms that support message replies.
type Replier interface {
	// SendReply sends a message as a reply to replyToID.
	// Returns the platform message ID of the sent message.
	SendReply(ctx context.Context, channel, text, replyToID string) (string, error)
}

// IDSender is an optional interface for platforms that can return message IDs.
type IDSender interface {
	// SendWithID sends a message and returns its platform message ID.
	SendWithID(ctx context.Context, channel, text string) (string, error)
}
