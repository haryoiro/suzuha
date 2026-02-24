package notification

import (
	"context"
	"fmt"

	pb "github.com/haryoiro/suzuha/gen/notification/v1"
	"google.golang.org/grpc"
)

// ReplyNotifier provides message sending with reply support and message ID tracking.
// It is a separate path from NotifyFunc, used only by tasks that need these capabilities.
type ReplyNotifier struct {
	client pb.NotificationServiceClient
}

// NewReplyNotifier creates a ReplyNotifier from a gRPC connection.
func NewReplyNotifier(conn grpc.ClientConnInterface) *ReplyNotifier {
	return &ReplyNotifier{client: pb.NewNotificationServiceClient(conn)}
}

// Notify sends a message and returns the platform message ID.
func (n *ReplyNotifier) Notify(ctx context.Context, channelID, content, source string) (string, error) {
	resp, err := n.client.SendMessage(ctx, &pb.SendMessageRequest{
		ChannelId: channelID,
		Content:   content,
		Source:    source,
	})
	if err != nil {
		return "", fmt.Errorf("reply_notifier: send: %w", err)
	}
	if !resp.Ok {
		return "", fmt.Errorf("reply_notifier: agent rejected: %s", resp.Error)
	}
	return resp.MessageId, nil
}

// Reply sends a reply to replyToID and returns the platform message ID.
func (n *ReplyNotifier) Reply(ctx context.Context, channelID, content, replyToID, source string) (string, error) {
	resp, err := n.client.SendMessage(ctx, &pb.SendMessageRequest{
		ChannelId:        channelID,
		Content:          content,
		Source:           source,
		ReplyToMessageId: replyToID,
	})
	if err != nil {
		return "", fmt.Errorf("reply_notifier: reply: %w", err)
	}
	if !resp.Ok {
		return "", fmt.Errorf("reply_notifier: agent rejected: %s", resp.Error)
	}
	return resp.MessageId, nil
}
