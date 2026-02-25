package notification

import (
	"context"
	"fmt"

	pb "github.com/haryoiro/suzuha/gen/notification/v1"
	"google.golang.org/grpc"
)

// GRPCNotifier implements Notifier via the Agent's gRPC NotificationService.
type GRPCNotifier struct {
	client pb.NotificationServiceClient
}

// NewGRPCNotifier creates a Notifier backed by a gRPC connection.
func NewGRPCNotifier(conn grpc.ClientConnInterface) *GRPCNotifier {
	return &GRPCNotifier{client: pb.NewNotificationServiceClient(conn)}
}

func (n *GRPCNotifier) Send(ctx context.Context, channelID, content, source string) (SendResult, error) {
	return n.send(ctx, channelID, content, "", source)
}

func (n *GRPCNotifier) Reply(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error) {
	return n.send(ctx, channelID, content, replyToID, source)
}

func (n *GRPCNotifier) send(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error) {
	resp, err := n.client.SendMessage(ctx, &pb.SendMessageRequest{
		ChannelId:        channelID,
		Content:          content,
		Source:           source,
		ReplyToMessageId: replyToID,
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("notification: send: %w", err)
	}
	if !resp.Ok {
		return SendResult{}, fmt.Errorf("notification: agent rejected: %s", resp.Error)
	}
	return SendResult{MessageID: resp.MessageId}, nil
}
