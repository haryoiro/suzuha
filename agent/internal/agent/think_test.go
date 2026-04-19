package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/runtime/event"
	"github.com/haryoiro/suzuha/internal/llm"
)

func TestConversationState(t *testing.T) {
	t.Run("bot never spoke", func(t *testing.T) {
		ag := newTestAgent()
		ch := "ch1"

		for i := 0; i < 5; i++ {
			ag.AgentContext().Add(llm.Message{
				Role: "user", UserID: "user1", Channel: ch,
				Timestamp: time.Now().Add(-time.Duration(5-i) * time.Minute),
			})
		}

		cs := ag.conversationState(ch)
		if cs.messagesSinceBotSpoke < convScanLimit {
			t.Errorf("expected messagesSinceBotSpoke >= %d when bot never spoke, got %d",
				convScanLimit, cs.messagesSinceBotSpoke)
		}
	})

	t.Run("bot spoke recently", func(t *testing.T) {
		ag := newTestAgent()
		ch := "ch1"

		ag.AgentContext().Add(llm.Message{
			Role: "assistant", UserID: "bot123", Channel: ch,
			Timestamp: time.Now().Add(-30 * time.Second),
		})
		ag.AgentContext().Add(llm.Message{
			Role: "user", UserID: "user1", Channel: ch,
			Timestamp: time.Now().Add(-20 * time.Second),
		})
		ag.AgentContext().Add(llm.Message{
			Role: "user", UserID: "user1", Channel: ch,
			Timestamp: time.Now().Add(-10 * time.Second),
		})

		cs := ag.conversationState(ch)
		if cs.botLastSpokeAgo > time.Minute {
			t.Errorf("expected botLastSpokeAgo < 1min, got %v", cs.botLastSpokeAgo)
		}
		if cs.messagesSinceBotSpoke != 2 {
			t.Errorf("expected messagesSinceBotSpoke = 2, got %d", cs.messagesSinceBotSpoke)
		}
		if cs.recentDistinctUsers != 1 {
			t.Errorf("expected recentDistinctUsers = 1, got %d", cs.recentDistinctUsers)
		}
	})

	t.Run("multiple users", func(t *testing.T) {
		ag := newTestAgent()
		ch := "ch1"

		ag.AgentContext().Add(llm.Message{
			Role: "assistant", UserID: "bot123", Channel: ch,
			Timestamp: time.Now().Add(-60 * time.Second),
		})
		ag.AgentContext().Add(llm.Message{
			Role: "user", UserID: "user1", Channel: ch,
			Timestamp: time.Now().Add(-30 * time.Second),
		})
		ag.AgentContext().Add(llm.Message{
			Role: "user", UserID: "user2", Channel: ch,
			Timestamp: time.Now().Add(-20 * time.Second),
		})

		cs := ag.conversationState(ch)
		if cs.recentDistinctUsers != 2 {
			t.Errorf("expected recentDistinctUsers = 2, got %d", cs.recentDistinctUsers)
		}
	})

	t.Run("different channel", func(t *testing.T) {
		ag := newTestAgent()

		ag.AgentContext().Add(llm.Message{
			Role: "assistant", UserID: "bot123", Channel: "ch1",
			Timestamp: time.Now().Add(-30 * time.Second),
		})
		ag.AgentContext().Add(llm.Message{
			Role: "user", UserID: "user1", Channel: "ch2",
			Timestamp: time.Now().Add(-10 * time.Second),
		})

		cs := ag.conversationState("ch2")
		if cs.messagesSinceBotSpoke < convScanLimit {
			t.Errorf("expected messagesSinceBotSpoke >= %d for ch2, got %d",
				convScanLimit, cs.messagesSinceBotSpoke)
		}
	})
}

func TestResponseDirective(t *testing.T) {
	tests := []struct {
		name         string
		event        event.Event
		cs           convState
		es           episodeSig
		wantPrefix   string
		wantContains string
	}{
		{
			"directly addressed",
			makeMentionEvent("hello", "ch1", "user1"),
			convState{},
			episodeSig{},
			"[RESPOND]",
			"",
		},
		{
			"active conversation",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       30 * time.Second,
				messagesSinceBotSpoke: 1,
				recentDistinctUsers:   1,
			},
			episodeSig{},
			"[RESPOND]",
			"会話の続き",
		},
		{
			"recent conversation 1-on-1",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       3 * time.Minute,
				messagesSinceBotSpoke: 4,
				recentDistinctUsers:   1,
			},
			episodeSig{},
			"[LISTEN]",
			"最近この会話に参加",
		},
		{
			"recent conversation multi-user",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       3 * time.Minute,
				messagesSinceBotSpoke: 4,
				recentDistinctUsers:   2,
			},
			episodeSig{},
			"",
			"チャンネルの会話",
		},
		{
			"close relationship",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       10 * time.Minute,
				messagesSinceBotSpoke: 20,
				recentDistinctUsers:   1,
			},
			episodeSig{count: 5, hasRecent: true},
			"",
			"仲の良い人",
		},
		{
			"acquaintance",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       10 * time.Minute,
				messagesSinceBotSpoke: 20,
				recentDistinctUsers:   1,
			},
			episodeSig{count: 2, hasRecent: false},
			"",
			"知り合い",
		},
		{
			"fallback default",
			makeMessageEvent("hi", "ch1", "user1"),
			convState{
				botLastSpokeAgo:       10 * time.Minute,
				messagesSinceBotSpoke: 20,
				recentDistinctUsers:   1,
			},
			episodeSig{},
			"",
			"チャンネルの会話",
		},
		{
			"self-prompt bypass",
			event.NewSelfPromptEvent("ch1", "bored"),
			convState{},
			episodeSig{},
			"[RESPOND]",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := responseDirective(tt.event, "bot123", tt.cs, tt.es)
			if tt.wantPrefix != "" && !strings.HasPrefix(d, tt.wantPrefix) {
				t.Errorf("expected prefix %q, got: %s", tt.wantPrefix, d)
			}
			if tt.wantContains != "" && !strings.Contains(d, tt.wantContains) {
				t.Errorf("expected to contain %q, got: %s", tt.wantContains, d)
			}
		})
	}

	t.Run("active but too many messages", func(t *testing.T) {
		evt := makeMessageEvent("hi", "ch1", "user1")
		cs := convState{
			botLastSpokeAgo:       30 * time.Second,
			messagesSinceBotSpoke: 10,
			recentDistinctUsers:   1,
		}
		d := responseDirective(evt, "bot123", cs, episodeSig{})
		if strings.Contains(d, "会話の続き") || strings.Contains(d, "最近この会話に参加") {
			t.Errorf("expected fallback, not active conversation directive, got: %s", d)
		}
	})
}
