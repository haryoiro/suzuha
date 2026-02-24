package notification

import (
	"context"
	"log/slog"

	pb "github.com/haryoiro/suzuha/gen/notification/v1"
	"github.com/haryoiro/suzuha/internal/chat"
)

// Server implements the gRPC NotificationService on the Agent side.
// It delegates message sending to a chat.Interface.
type Server struct {
	pb.UnimplementedNotificationServiceServer
	chat   chat.Interface
	logger *slog.Logger
}

// NewServer creates a notification gRPC server.
func NewServer(chatIface chat.Interface, logger *slog.Logger) *Server {
	return &Server{chat: chatIface, logger: logger}
}

// SendMessage sends a message to a Discord channel via the chat interface.
// If reply_to_message_id is set and the chat platform supports replies,
// the message is sent as a reply. Message IDs are returned when available.
func (s *Server) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	s.logger.Info("notification: sending message",
		"channel", req.ChannelId,
		"source", req.Source,
		"content_len", len(req.Content),
		"reply_to", req.ReplyToMessageId,
	)

	var msgID string
	var err error

	switch {
	case req.ReplyToMessageId != "":
		if r, ok := s.chat.(chat.Replier); ok {
			msgID, err = r.SendReply(ctx, req.ChannelId, req.Content, req.ReplyToMessageId)
		} else {
			// Fallback: send without reply if platform doesn't support it.
			err = s.chat.Send(ctx, req.ChannelId, req.Content)
		}
	default:
		if idS, ok := s.chat.(chat.IDSender); ok {
			msgID, err = idS.SendWithID(ctx, req.ChannelId, req.Content)
		} else {
			err = s.chat.Send(ctx, req.ChannelId, req.Content)
		}
	}

	if err != nil {
		s.logger.Warn("notification: send failed", "error", err)
		return &pb.SendMessageResponse{
			Ok:    false,
			Error: err.Error(),
		}, nil
	}

	return &pb.SendMessageResponse{
		Ok:        true,
		MessageId: msgID,
	}, nil
}
