package notification

import (
	"context"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/chat"
)

// ChatNotifier implements Notifier by routing messages directly through
// a chat.Interface. This replaces the gRPC-based notification path
// used when consolidator ran as a separate process.
type ChatNotifier struct {
	chat   chat.Interface
	logger *slog.Logger
}

// NewChatNotifier creates a Notifier backed by a chat.Interface.
func NewChatNotifier(chatIface chat.Interface, logger *slog.Logger) *ChatNotifier {
	return &ChatNotifier{chat: chatIface, logger: logger}
}

func (n *ChatNotifier) Send(ctx context.Context, channelID, content, source string) (SendResult, error) {
	n.logger.Info("notification: メッセージを送信中",
		"channel", channelID,
		"source", source,
		"content_len", len(content),
	)

	if idS, ok := n.chat.(chat.IDSender); ok {
		msgID, err := idS.SendWithID(ctx, channelID, content)
		if err != nil {
			n.logger.Warn("notification: 送信に失敗しました", "error", err)
			return SendResult{}, err
		}
		return SendResult{MessageID: msgID}, nil
	}

	if err := n.chat.Send(ctx, channelID, content); err != nil {
		n.logger.Warn("notification: 送信に失敗しました", "error", err)
		return SendResult{}, err
	}
	return SendResult{}, nil
}

func (n *ChatNotifier) Reply(ctx context.Context, channelID, content, replyToID, source string) (SendResult, error) {
	n.logger.Info("notification: リプライを送信中",
		"channel", channelID,
		"source", source,
		"content_len", len(content),
		"reply_to", replyToID,
	)

	if replyToID != "" {
		if r, ok := n.chat.(chat.Replier); ok {
			msgID, err := r.SendReply(ctx, channelID, content, replyToID)
			if err != nil {
				n.logger.Warn("notification: リプライに失敗しました", "error", err)
				return SendResult{}, err
			}
			return SendResult{MessageID: msgID}, nil
		}
	}

	// Fallback to regular send if platform doesn't support replies.
	return n.Send(ctx, channelID, content, source)
}

// Ensure ChatNotifier implements Notifier at compile time.
var _ Notifier = (*ChatNotifier)(nil)
