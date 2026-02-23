// Package notification provides gRPC-based notification services for sending
// messages to chat channels from external processes.
package notification

import (
	"context"
	"fmt"

	pb "github.com/haryoiro/suzuha/gen/notification/v1"
	"google.golang.org/grpc"
)

// NotifyFunc sends a notification to a chat channel.
// channelID identifies the destination (e.g. Discord channel ID).
// content is the message text. source labels the origin (e.g. "rss", "todo").
type NotifyFunc func(ctx context.Context, channelID, content, source string) error

// NewGRPCNotifier creates a NotifyFunc backed by a gRPC connection to the Agent's
// NotificationService.
func NewGRPCNotifier(conn grpc.ClientConnInterface) NotifyFunc {
	client := pb.NewNotificationServiceClient(conn)
	return func(ctx context.Context, channelID, content, source string) error {
		resp, err := client.SendMessage(ctx, &pb.SendMessageRequest{
			ChannelId: channelID,
			Content:   content,
			Source:    source,
		})
		if err != nil {
			return fmt.Errorf("notification: send: %w", err)
		}
		if !resp.Ok {
			return fmt.Errorf("notification: agent rejected: %s", resp.Error)
		}
		return nil
	}
}

// Nop returns a NotifyFunc that does nothing. Useful for testing.
func Nop() NotifyFunc {
	return func(ctx context.Context, channelID, content, source string) error {
		return nil
	}
}
