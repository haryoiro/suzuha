package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
)

func TestConversationState_BotNeverSpoke(t *testing.T) {
	ag := newTestAgent()
	ch := "ch1"

	// Add some user messages, no bot messages.
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
}

func TestConversationState_BotSpokeRecently(t *testing.T) {
	ag := newTestAgent()
	ch := "ch1"

	// Bot spoke 30 seconds ago.
	ag.AgentContext().Add(llm.Message{
		Role: "assistant", UserID: "bot123", Channel: ch,
		Timestamp: time.Now().Add(-30 * time.Second),
	})
	// 2 user messages after.
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
}

func TestConversationState_MultipleUsers(t *testing.T) {
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
}

func TestConversationState_DifferentChannel(t *testing.T) {
	ag := newTestAgent()

	// Bot spoke in ch1 recently.
	ag.AgentContext().Add(llm.Message{
		Role: "assistant", UserID: "bot123", Channel: "ch1",
		Timestamp: time.Now().Add(-30 * time.Second),
	})
	// User message in ch2.
	ag.AgentContext().Add(llm.Message{
		Role: "user", UserID: "user1", Channel: "ch2",
		Timestamp: time.Now().Add(-10 * time.Second),
	})

	cs := ag.conversationState("ch2")
	// Bot never spoke in ch2, so messagesSinceBotSpoke should be large.
	if cs.messagesSinceBotSpoke < convScanLimit {
		t.Errorf("expected messagesSinceBotSpoke >= %d for ch2, got %d",
			convScanLimit, cs.messagesSinceBotSpoke)
	}
}

func TestResponseDirective_DirectlyAddressed(t *testing.T) {
	evt := makeMentionEvent("hello", "ch1", "user1")
	d := responseDirective(evt, "bot123", convState{}, episodeSig{})
	if !strings.HasPrefix(d, "[RESPOND]") {
		t.Errorf("expected [RESPOND] for mention, got: %s", d)
	}
}

func TestResponseDirective_ActiveConversation(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       30 * time.Second,
		messagesSinceBotSpoke: 1,
		recentDistinctUsers:   1,
	}
	d := responseDirective(evt, "bot123", cs, episodeSig{})
	if !strings.HasPrefix(d, "[RESPOND]") {
		t.Errorf("expected [RESPOND] for active conversation, got: %s", d)
	}
	if !strings.Contains(d, "会話の続き") {
		t.Errorf("expected active conversation text, got: %s", d)
	}
}

func TestResponseDirective_RecentConversation1on1(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       3 * time.Minute,
		messagesSinceBotSpoke: 4,
		recentDistinctUsers:   1, // 1-on-1
	}
	d := responseDirective(evt, "bot123", cs, episodeSig{})
	if !strings.HasPrefix(d, "[LISTEN]") {
		t.Errorf("expected [LISTEN] for recent 1-on-1, got: %s", d)
	}
	if !strings.Contains(d, "最近この会話に参加") {
		t.Errorf("expected recent conversation text, got: %s", d)
	}
}

func TestResponseDirective_RecentConversationMultiUser(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       3 * time.Minute,
		messagesSinceBotSpoke: 4,
		recentDistinctUsers:   2, // not 1-on-1
	}
	// Should fall through to episode or default.
	d := responseDirective(evt, "bot123", cs, episodeSig{})
	if !strings.Contains(d, "チャンネルの会話") {
		t.Errorf("expected channel directive for multi-user with no episodes, got: %s", d)
	}
}

func TestResponseDirective_CloseRelationship(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       10 * time.Minute,
		messagesSinceBotSpoke: 20,
		recentDistinctUsers:   1,
	}
	es := episodeSig{count: 5, hasRecent: true}
	d := responseDirective(evt, "bot123", cs, es)
	if !strings.Contains(d, "仲の良い人") {
		t.Errorf("expected close relationship directive, got: %s", d)
	}
}

func TestResponseDirective_Acquaintance(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       10 * time.Minute,
		messagesSinceBotSpoke: 20,
		recentDistinctUsers:   1,
	}
	es := episodeSig{count: 2, hasRecent: false}
	d := responseDirective(evt, "bot123", cs, es)
	if !strings.Contains(d, "知り合い") {
		t.Errorf("expected acquaintance directive, got: %s", d)
	}
}

func TestResponseDirective_FallbackDefault(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       10 * time.Minute,
		messagesSinceBotSpoke: 20,
		recentDistinctUsers:   1,
	}
	d := responseDirective(evt, "bot123", cs, episodeSig{})
	if !strings.Contains(d, "チャンネルの会話") {
		t.Errorf("expected default channel directive, got: %s", d)
	}
}

func TestResponseDirective_ActiveButTooManyMessages(t *testing.T) {
	evt := makeMessageEvent("hi", "ch1", "user1")
	cs := convState{
		botLastSpokeAgo:       30 * time.Second,
		messagesSinceBotSpoke: 10, // exceeds convActiveMaxMsgs
		recentDistinctUsers:   1,
	}
	// Should NOT match priority 2 or 3 (too many messages for both).
	d := responseDirective(evt, "bot123", cs, episodeSig{})
	if strings.Contains(d, "会話の続き") || strings.Contains(d, "最近この会話に参加") {
		t.Errorf("expected fallback, not active conversation directive, got: %s", d)
	}
}

func TestResponseDirective_SelfPromptBypass(t *testing.T) {
	// Self-prompts are handled in Think before calling responseDirective,
	// but isDirectlyAddressed returns true for them.
	evt := event.NewSelfPromptEvent("ch1", "bored")
	d := responseDirective(evt, "bot123", convState{}, episodeSig{})
	if !strings.HasPrefix(d, "[RESPOND]") {
		t.Errorf("expected [RESPOND] for self-prompt, got: %s", d)
	}
}
