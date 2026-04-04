package agent

import (
	"context"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
	acq "github.com/haryoiro/suzuha/internal/memento/acquirer"
	"github.com/haryoiro/suzuha/internal/memory"
)

func TestDoCompactWith_ClearsAllMessages(t *testing.T) {
	ag := newTestAgent()
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	// Add messages to context.
	for i := 0; i < 10; i++ {
		agentCtx.Add(llm.Message{Role: "user", Content: "msg"})
	}
	if got := len(agentCtx.Messages()); got != 10 {
		t.Fatalf("setup: expected 10 messages, got %d", got)
	}

	msgs := agentCtx.Messages()
	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)

	if got := len(agentCtx.Messages()); got != 0 {
		t.Errorf("expected 0 messages after compaction, got %d", got)
	}
}

func TestDoCompactWith_AsyncPreservesNewMessages(t *testing.T) {
	ag := newTestAgent()
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	// Add messages to context.
	for i := 0; i < 10; i++ {
		agentCtx.Add(llm.Message{Role: "user", Content: "old"})
	}

	// Take snapshot (simulates what compactAsyncFor does).
	snapshot := agentCtx.Messages()

	// Simulate messages arriving during compaction.
	agentCtx.Add(llm.Message{Role: "user", Content: "new1"})
	agentCtx.Add(llm.Message{Role: "user", Content: "new2"})

	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, snapshot, true)

	msgs := agentCtx.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (new ones), got %d", len(msgs))
	}
	if msgs[0].Content != "new1" || msgs[1].Content != "new2" {
		t.Errorf("expected new1/new2, got %q/%q", msgs[0].Content, msgs[1].Content)
	}
}

func TestDoCompactWith_ResetsSeenChannels(t *testing.T) {
	ag := newTestAgent()
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	agentCtx.MarkChannelSeen("ch1")
	if !agentCtx.HasChannelHistory("ch1") {
		t.Fatal("setup: channel should be seen")
	}

	agentCtx.Add(llm.Message{Role: "user", Content: "msg"})
	msgs := agentCtx.Messages()
	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)

	if agentCtx.HasChannelHistory("ch1") {
		t.Error("seen channels should be reset after compaction")
	}
}

func TestDoCompactWith_ResetsInjectedUsers(t *testing.T) {
	ag := newTestAgent()
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	agentCtx.MarkUserProfileInjected("user1")
	if !agentCtx.HasUserProfile("user1") {
		t.Fatal("setup: user should be injected")
	}

	agentCtx.Add(llm.Message{Role: "user", Content: "msg"})
	msgs := agentCtx.Messages()
	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)

	if agentCtx.HasUserProfile("user1") {
		t.Error("injected users should be reset after compaction")
	}
}

// trackingAcquirer records whether Acquire was called and what was passed.
type trackingAcquirer struct {
	called   bool
	messages []llm.Message
}

func (c *trackingAcquirer) Acquire(_ context.Context, req *acq.AcquireRequest) (*acq.AcquireResult, error) {
	c.called = true
	c.messages = req.Messages
	return &acq.AcquireResult{
		Memories: []memory.Memory{
			{Type: memory.MemoryTypeWorld, Content: "extracted"},
		},
	}, nil
}

func TestDoCompactWith_CallsAcquirer(t *testing.T) {
	tc := &trackingAcquirer{}
	ag := newTestAgent(func(a *Agent) {
		a.acquirer = tc
	})
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	agentCtx.Add(llm.Message{Role: "user", Content: "hello"})
	agentCtx.Add(llm.Message{Role: "assistant", Content: "hi"})
	msgs := agentCtx.Messages()

	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)

	if !tc.called {
		t.Error("acquirer should have been called")
	}
	if len(tc.messages) != 2 {
		t.Errorf("acquirer should receive 2 messages, got %d", len(tc.messages))
	}
	// Context should still be cleared.
	if got := len(agentCtx.Messages()); got != 0 {
		t.Errorf("expected 0 messages after compaction, got %d", got)
	}
}

func TestDoCompactWith_NoAcquirer(t *testing.T) {
	ag := newTestAgent(func(a *Agent) {
		a.acquirer = nil
	})
	ctx := context.Background()
	agentCtx := ag.contexts[SourceKeyDiscord]

	for i := 0; i < 5; i++ {
		agentCtx.Add(llm.Message{Role: "user", Content: "msg"})
	}
	msgs := agentCtx.Messages()

	ag.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)

	// Should still clear even without acquirer.
	if got := len(agentCtx.Messages()); got != 0 {
		t.Errorf("expected 0 messages, got %d", got)
	}
}

func TestCompactAsyncFor_SkipsConcurrent(t *testing.T) {
	// Use a slow acquirer to test concurrent skip.
	slow := &slowAcquirer{delay: 100 * time.Millisecond}
	ag := newTestAgent(func(a *Agent) {
		a.acquirer = slow
	})
	agentCtx := ag.contexts[SourceKeyDiscord]

	for i := 0; i < 5; i++ {
		agentCtx.Add(llm.Message{Role: "user", Content: "msg"})
	}

	// First call should start compaction.
	ag.compactAsyncFor(context.Background(), agentCtx, SourceKeyDiscord)

	// Second call should be skipped (mutex held).
	ag.compactAsyncFor(context.Background(), agentCtx, SourceKeyDiscord)

	// Wait for compaction to finish.
	time.Sleep(200 * time.Millisecond)

	if slow.callCount != 1 {
		t.Errorf("expected acquirer called once, got %d", slow.callCount)
	}
}

type slowAcquirer struct {
	delay     time.Duration
	callCount int
}

func (c *slowAcquirer) Acquire(_ context.Context, _ *acq.AcquireRequest) (*acq.AcquireResult, error) {
	c.callCount++
	time.Sleep(c.delay)
	return &acq.AcquireResult{}, nil
}
