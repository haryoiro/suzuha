package notification

import (
	"context"
	"log/slog"
	"testing"
)

// mockChat implements chat.Sender for testing.
type mockChat struct {
	sentMessages []mockSend
}

type mockSend struct {
	Channel string
	Text    string
}

func (m *mockChat) Send(_ context.Context, channel, text string) error {
	m.sentMessages = append(m.sentMessages, mockSend{Channel: channel, Text: text})
	return nil
}

// mockChatWithReply implements chat.Sender + chat.Replier + chat.IDSender.
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

func TestChatNotifier_Send_Basic(t *testing.T) {
	ch := &mockChat{}
	n := NewChatNotifier(ch, slog.Default())

	result, err := n.Send(context.Background(), "ch1", "hello", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "" {
		t.Errorf("expected empty message_id for basic send, got %q", result.MessageID)
	}
	if len(ch.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(ch.sentMessages))
	}
	if ch.sentMessages[0].Text != "hello" {
		t.Errorf("text: got %q", ch.sentMessages[0].Text)
	}
}

func TestChatNotifier_Send_IDSender(t *testing.T) {
	ch := &mockChatWithReply{nextMsgID: "sent-msg-id"}
	n := NewChatNotifier(ch, slog.Default())

	result, err := n.Send(context.Background(), "ch1", "normal message", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "sent-msg-id" {
		t.Errorf("message_id: got %q, want %q", result.MessageID, "sent-msg-id")
	}
	if len(ch.sentWithID) != 1 {
		t.Fatalf("expected 1 sendWithID, got %d", len(ch.sentWithID))
	}
}

func TestChatNotifier_Reply_WithReplier(t *testing.T) {
	ch := &mockChatWithReply{nextMsgID: "new-msg-id"}
	n := NewChatNotifier(ch, slog.Default())

	result, err := n.Reply(context.Background(), "ch1", "reply text", "original-msg-id", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageID != "new-msg-id" {
		t.Errorf("message_id: got %q, want %q", result.MessageID, "new-msg-id")
	}
	if len(ch.sentReplies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(ch.sentReplies))
	}
	if ch.sentReplies[0].ReplyToID != "original-msg-id" {
		t.Errorf("reply_to: got %q", ch.sentReplies[0].ReplyToID)
	}
}

func TestChatNotifier_Reply_Fallback(t *testing.T) {
	// Plain chat without Replier interface → falls back to Send.
	ch := &mockChat{}
	n := NewChatNotifier(ch, slog.Default())

	result, err := n.Reply(context.Background(), "ch1", "reply attempt", "some-msg-id", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls back to basic Send — no message_id available.
	if result.MessageID != "" {
		t.Errorf("expected empty message_id for fallback, got %q", result.MessageID)
	}
	if len(ch.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message (fallback), got %d", len(ch.sentMessages))
	}
}
