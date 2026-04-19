package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/haryoiro/suzuha/internal/llm"
)

func TestChannelProvider_ProvideContext(t *testing.T) {
	tests := []struct {
		name           string
		req            Request
		wantBackground int
		wantForeground int
	}{
		{
			"non-discord source returns empty",
			Request{Source: "cli", Channel: "ch1"},
			0, 0,
		},
		{
			"discord with no other channels",
			Request{
				Source:  "discord",
				Channel: "ch1",
				Messages: []llm.Message{
					{Role: "user", Channel: "ch1", Content: "hello"},
				},
			},
			0, 0,
		},
		{
			"discord with other channels",
			Request{
				Source:  "discord",
				Channel: "ch1",
				Messages: []llm.Message{
					{Role: "user", Channel: "ch2", ChannelName: "general", GuildID: "g1", Content: "hello"},
				},
			},
			1, 0,
		},
		{
			"home channel adds foreground",
			Request{Source: "discord", Channel: "ch1", IsHome: true},
			0, 1,
		},
		{
			"discord with other channels and home",
			Request{
				Source:  "discord",
				Channel: "ch1",
				IsHome:  true,
				Messages: []llm.Message{
					{Role: "user", Channel: "ch2", ChannelName: "random", GuildID: "g1", Content: "hey"},
				},
			},
			1, 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ChannelProvider{}
			block := p.ProvideContext(context.Background(), tt.req)
			if len(block.Background) != tt.wantBackground {
				t.Errorf("Background messages = %d, want %d", len(block.Background), tt.wantBackground)
			}
			if len(block.Foreground) != tt.wantForeground {
				t.Errorf("Foreground messages = %d, want %d", len(block.Foreground), tt.wantForeground)
			}
		})
	}
}

func TestBuildOtherChannels(t *testing.T) {
	tests := []struct {
		name           string
		msgs           []llm.Message
		currentChannel string
		wantEmpty      bool
		wantContains   []string
	}{
		{
			"no messages",
			nil,
			"ch1",
			true,
			nil,
		},
		{
			"only current channel messages",
			[]llm.Message{
				{Role: "user", Channel: "ch1", Content: "hello"},
			},
			"ch1",
			true,
			nil,
		},
		{
			"system messages excluded",
			[]llm.Message{
				{Role: "system", Channel: "ch2", Content: "prompt"},
			},
			"ch1",
			true,
			nil,
		},
		{
			"guild channel listed",
			[]llm.Message{
				{Role: "user", Channel: "ch2", ChannelName: "general", GuildID: "g1", Content: "hey"},
			},
			"ch1",
			false,
			[]string{"#general", "ch2"},
		},
		{
			"DM channel listed",
			[]llm.Message{
				{Role: "user", Channel: "dm1", UserID: "u1", Content: "hi"},
			},
			"ch1",
			false,
			[]string{"DM:", "u1"},
		},
		{
			"messages with empty channel skipped",
			[]llm.Message{
				{Role: "user", Channel: "", Content: "orphan"},
			},
			"ch1",
			true,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOtherChannels(tt.msgs, tt.currentChannel)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected non-empty string, got empty")
			}
			for _, s := range tt.wantContains {
				if !strings.Contains(got, s) {
					t.Errorf("expected result to contain %q, got:\n%s", s, got)
				}
			}
		})
	}
}
