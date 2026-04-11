package prompt

import (
	"context"
	"testing"

	"github.com/haryoiro/suzuha/internal/event"
)

func TestSelfPromptProvider_ProvideContext(t *testing.T) {
	tests := []struct {
		name           string
		req            Request
		wantForeground int
		wantContent    string
	}{
		{
			"self_prompt event returns foreground message",
			Request{EventType: event.TypeSelfPrompt, EventContent: "暇だから話そう"},
			1,
			"暇だから話そう",
		},
		{
			"message event returns empty block",
			Request{EventType: "message", EventContent: "hello"},
			0,
			"",
		},
		{
			"empty event type returns empty block",
			Request{EventType: "", EventContent: "test"},
			0,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := SelfPromptProvider{}
			block := p.ProvideContext(context.Background(), tt.req)
			if len(block.Foreground) != tt.wantForeground {
				t.Errorf("Foreground messages = %d, want %d", len(block.Foreground), tt.wantForeground)
			}
			if tt.wantForeground > 0 && block.Foreground[0].Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", block.Foreground[0].Content, tt.wantContent)
			}
		})
	}
}
