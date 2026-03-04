package agent

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/event"
)

func TestShouldRespond_CLI(t *testing.T) {
	e := event.NewMessageEvent("cli", event.MessagePayload{Content: "hello"})
	if !ShouldRespond(e, "bot123") {
		t.Error("CLI events should always trigger a response")
	}
}

func TestShouldRespond_Trigger(t *testing.T) {
	e := event.Event{Source: "timer", Type: "trigger"}
	if !ShouldRespond(e, "bot123") {
		t.Error("trigger events should always trigger a response")
	}
}

func TestShouldRespond_DM(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "hi", IsDM: true,
	})
	if !ShouldRespond(e, "bot123") {
		t.Error("DM events should always trigger a response")
	}
}

func TestShouldRespond_Mention(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "hey", IsMention: true,
	})
	if !ShouldRespond(e, "bot123") {
		t.Error("mention events should always trigger a response")
	}
}

func TestIsDirectlyAddressed_DM(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "hello", IsDM: true,
	})
	if !isDirectlyAddressed(e, "bot123") {
		t.Error("DM should be directly addressed")
	}
}

func TestIsDirectlyAddressed_Mention(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "hey", IsMention: true,
	})
	if !isDirectlyAddressed(e, "bot123") {
		t.Error("mention should be directly addressed")
	}
}

func TestIsDirectlyAddressed_RegularMessage(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "some random message", Channel: "general",
	})
	if isDirectlyAddressed(e, "bot123") {
		t.Error("regular channel message should NOT be directly addressed")
	}
}

func TestIsDirectlyAddressed_BotMentionInContent(t *testing.T) {
	e := event.NewMessageEvent("discord", event.MessagePayload{
		Content: "hello <@bot123> how are you", Channel: "general",
	})
	if !isDirectlyAddressed(e, "bot123") {
		t.Error("content with <@botID> should be directly addressed")
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
