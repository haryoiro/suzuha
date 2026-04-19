package agent

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/runtime/event"
)

func TestShouldRespond(t *testing.T) {
	tests := []struct {
		name  string
		event event.Event
		want  bool
	}{
		{
			"CLI event",
			event.NewMessageEvent("cli", event.MessagePayload{Content: "hello"}),
			true,
		},
		{
			"trigger event",
			event.Event{Source: "timer", Type: "trigger"},
			true,
		},
		{
			"DM event",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "hi", IsDM: true}),
			true,
		},
		{
			"mention event",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "hey", IsMention: true}),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldRespond(tt.event, "bot123"); got != tt.want {
				t.Errorf("ShouldRespond() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDirectlyAddressed(t *testing.T) {
	tests := []struct {
		name  string
		event event.Event
		want  bool
	}{
		{
			"DM",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "hello", IsDM: true}),
			true,
		},
		{
			"mention",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "hey", IsMention: true}),
			true,
		},
		{
			"regular message",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "some random message", Channel: "general"}),
			false,
		},
		{
			"bot mention in content",
			event.NewMessageEvent("discord", event.MessagePayload{Content: "hello <@bot123> how are you", Channel: "general"}),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDirectlyAddressed(tt.event, "bot123"); got != tt.want {
				t.Errorf("isDirectlyAddressed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsBotMention(t *testing.T) {
	tests := []struct {
		name    string
		content string
		botID   string
		want    bool
	}{
		{"exact mention", "hello <@bot123>", "bot123", true},
		{"mention in middle", "hey <@bot123> sup", "bot123", true},
		{"no mention", "hello world", "bot123", false},
		{"partial match", "hello <@bot12", "bot123", false},
		{"empty content", "", "bot123", false},
		{"empty botID", "hello <@bot123>", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsBotMention(tt.content, tt.botID); got != tt.want {
				t.Errorf("containsBotMention(%q, %q) = %v, want %v", tt.content, tt.botID, got, tt.want)
			}
		})
	}
}
