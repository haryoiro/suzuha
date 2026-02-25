// Package notification provides gRPC-based notification services for sending
// messages to chat channels from external processes.
package notification

import "context"

// SendResult holds the outcome of a notification send.
type SendResult struct {
	MessageID string // Platform message ID. Empty if not available or suppressed.
}

// Notifier sends messages to chat channels. All scheduler tasks use this
// single interface for both regular sends and replies.
type Notifier interface {
	// Send sends a message to a channel and returns the platform message ID.
	Send(ctx context.Context, channelID, content, source string) (SendResult, error)

	// Reply sends a message as a reply to an existing message.
	// If the platform doesn't support replies, it falls back to a regular send.
	Reply(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error)
}

// NopNotifier is a Notifier that does nothing. Useful for testing.
type NopNotifier struct{}

func (NopNotifier) Send(_ context.Context, _, _, _ string) (SendResult, error) {
	return SendResult{}, nil
}

func (NopNotifier) Reply(_ context.Context, _, _, _, _ string) (SendResult, error) {
	return SendResult{}, nil
}
