package notification

import (
	"context"
	"log/slog"
	"testing"

	pb "github.com/haryoiro/suzuha/gen/notification/v1"
)

// mockChat implements chat.Interface for testing.
type mockChat struct {
	sentMessages []mockSend
}

type mockSend struct {
	Channel string
	Text    string
}

func (m *mockChat) Run(_ context.Context) error { return nil }

func (m *mockChat) Send(_ context.Context, channel, text string) error {
	m.sentMessages = append(m.sentMessages, mockSend{Channel: channel, Text: text})
	return nil
}

// mockChatWithReply implements chat.Interface + chat.Replier + chat.IDSender.
type mockChatWithReply struct {
	mockChat
	sentReplies []mockReply
	sentWithID  []mockSendWithID
	nextMsgID   string
}

type mockReply struct {
	Channel   string
	Text      string
	ReplyToID string
}

type mockSendWithID struct {
	Channel string
	Text    string
}

func (m *mockChatWithReply) SendReply(_ context.Context, channel, text, replyToID string) (string, error) {
	m.sentReplies = append(m.sentReplies, mockReply{Channel: channel, Text: text, ReplyToID: replyToID})
	return m.nextMsgID, nil
}

func (m *mockChatWithReply) SendWithID(_ context.Context, channel, text string) (string, error) {
	m.sentWithID = append(m.sentWithID, mockSendWithID{Channel: channel, Text: text})
	return m.nextMsgID, nil
}

func TestServer_SendMessage_Basic(t *testing.T) {
	chat := &mockChat{}
	srv := NewServer(chat, slog.Default())

	resp, err := srv.SendMessage(context.Background(), &pb.SendMessageRequest{
		ChannelId: "ch1",
		Content:   "hello",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Ok {
		t.Error("expected ok=true")
	}
	if len(chat.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(chat.sentMessages))
	}
	if chat.sentMessages[0].Text != "hello" {
		t.Errorf("text: got %q", chat.sentMessages[0].Text)
	}
}

func TestServer_SendMessage_WithReply(t *testing.T) {
	chat := &mockChatWithReply{nextMsgID: "new-msg-id"}
	srv := NewServer(chat, slog.Default())

	resp, err := srv.SendMessage(context.Background(), &pb.SendMessageRequest{
		ChannelId:        "ch1",
		Content:          "reply text",
		Source:           "test",
		ReplyToMessageId: "original-msg-id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Ok {
		t.Error("expected ok=true")
	}
	if resp.MessageId != "new-msg-id" {
		t.Errorf("message_id: got %q, want %q", resp.MessageId, "new-msg-id")
	}
	if len(chat.sentReplies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(chat.sentReplies))
	}
	if chat.sentReplies[0].ReplyToID != "original-msg-id" {
		t.Errorf("reply_to: got %q", chat.sentReplies[0].ReplyToID)
	}
}

func TestServer_SendMessage_IDSender(t *testing.T) {
	chat := &mockChatWithReply{nextMsgID: "sent-msg-id"}
	srv := NewServer(chat, slog.Default())

	// No reply_to → should use IDSender.
	resp, err := srv.SendMessage(context.Background(), &pb.SendMessageRequest{
		ChannelId: "ch1",
		Content:   "normal message",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.MessageId != "sent-msg-id" {
		t.Errorf("message_id: got %q, want %q", resp.MessageId, "sent-msg-id")
	}
	if len(chat.sentWithID) != 1 {
		t.Fatalf("expected 1 sendWithID, got %d", len(chat.sentWithID))
	}
}

func TestServer_SendMessage_ReplyFallback(t *testing.T) {
	// Plain chat without Replier interface → falls back to Send.
	chat := &mockChat{}
	srv := NewServer(chat, slog.Default())

	resp, err := srv.SendMessage(context.Background(), &pb.SendMessageRequest{
		ChannelId:        "ch1",
		Content:          "reply attempt",
		Source:           "test",
		ReplyToMessageId: "some-msg-id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Ok {
		t.Error("expected ok=true")
	}
	// Falls back to basic Send.
	if len(chat.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message (fallback), got %d", len(chat.sentMessages))
	}
	// No message_id since basic Send doesn't return one.
	if resp.MessageId != "" {
		t.Errorf("expected empty message_id for fallback, got %q", resp.MessageId)
	}
}
