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
func (s *Server) SendMessage(ctx context.Context, req *pb.SendMessageRequest) (*pb.SendMessageResponse, error) {
	s.logger.Info("notification: sending message",
		"channel", req.ChannelId,
		"source", req.Source,
		"content_len", len(req.Content),
	)

	if err := s.chat.Send(ctx, req.ChannelId, req.Content); err != nil {
		s.logger.Warn("notification: send failed", "error", err)
		return &pb.SendMessageResponse{
			Ok:    false,
			Error: err.Error(),
		}, nil
	}

	return &pb.SendMessageResponse{
		Ok: true,
	}, nil
}
