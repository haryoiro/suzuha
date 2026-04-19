package event

import "testing"

func TestNewMessageEvent(t *testing.T) {
	tests := []struct {
		name   string
		source string
		msg    MessagePayload
	}{
		{
			"discord message",
			"discord",
			MessagePayload{Content: "hello", Channel: "ch1", UserID: "u1", UserName: "alice"},
		},
		{
			"cli message",
			"cli",
			MessagePayload{Content: "test", Channel: "local", UserID: "dev"},
		},
		{
			"DM message",
			"discord",
			MessagePayload{Content: "secret", Channel: "dm1", UserID: "u2", IsDM: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := NewMessageEvent(tt.source, tt.msg)
			if ev.ID == "" {
				t.Error("expected non-empty ID")
			}
			if ev.Source != tt.source {
				t.Errorf("Source = %q, want %q", ev.Source, tt.source)
			}
			if ev.Type != "message" {
				t.Errorf("Type = %q, want %q", ev.Type, "message")
			}
			if ev.Message.Content != tt.msg.Content {
				t.Errorf("Message.Content = %q, want %q", ev.Message.Content, tt.msg.Content)
			}
			if ev.Timestamp.IsZero() {
				t.Error("expected non-zero Timestamp")
			}
		})
	}
}

func TestNewSelfPromptEvent(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		content string
	}{
		{"boredom trigger", "home", "暇だからなにか話そう"},
		{"empty content", "ch1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := NewSelfPromptEvent(tt.channel, tt.content)
			if ev.ID == "" {
				t.Error("expected non-empty ID")
			}
			if ev.Source != SourceInternal {
				t.Errorf("Source = %q, want %q", ev.Source, SourceInternal)
			}
			if ev.Type != TypeSelfPrompt {
				t.Errorf("Type = %q, want %q", ev.Type, TypeSelfPrompt)
			}
			if ev.Message.Channel != tt.channel {
				t.Errorf("Message.Channel = %q, want %q", ev.Message.Channel, tt.channel)
			}
			if ev.Message.Content != tt.content {
				t.Errorf("Message.Content = %q, want %q", ev.Message.Content, tt.content)
			}
		})
	}
}

func TestNewMessageEvent_UniqueIDs(t *testing.T) {
	msg := MessagePayload{Content: "test"}
	ev1 := NewMessageEvent("cli", msg)
	ev2 := NewMessageEvent("cli", msg)
	if ev1.ID == ev2.ID {
		t.Errorf("expected unique IDs, both got %q", ev1.ID)
	}
}

func TestBus_PublishSubscribe(t *testing.T) {
	tests := []struct {
		name       string
		bufferSize int
		eventCount int
	}{
		{"single event", 10, 1},
		{"multiple events", 10, 5},
		{"buffer exactly full", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := NewBus(tt.bufferSize)
			ch := bus.Subscribe()

			for i := 0; i < tt.eventCount; i++ {
				bus.Publish(NewMessageEvent("test", MessagePayload{Content: "msg"}))
			}

			for i := 0; i < tt.eventCount; i++ {
				select {
				case ev := <-ch:
					if ev.Type != "message" {
						t.Errorf("event %d: Type = %q, want %q", i, ev.Type, "message")
					}
				default:
					t.Errorf("event %d: expected event on channel, got nothing", i)
				}
			}

			select {
			case ev := <-ch:
				t.Errorf("expected no more events, got %+v", ev)
			default:
				// ok
			}
		})
	}
}
