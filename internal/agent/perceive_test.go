package agent

import (
	"context"
	"testing"

	"github.com/haryoiro/suzuha/internal/event"
)

func TestPerceive_SingleMessage(t *testing.T) {
	ag := newTestAgent()
	evt := makeMessageEvent("hello world", "ch1", "user1")

	p := ag.Perceive(context.Background(), []event.Event{evt})
	if p == nil {
		t.Fatal("Perceive returned nil for valid event")
	}

	if p.Channel != "ch1" {
		t.Errorf("Channel = %q, want %q", p.Channel, "ch1")
	}
	if p.LastMessage.Content != "hello world" {
		t.Errorf("LastMessage.Content = %q, want %q", p.LastMessage.Content, "hello world")
	}
	if p.DirectlyAddressed {
		t.Error("regular message should not be directly addressed")
	}
	if p.IsDM {
		t.Error("expected IsDM=false for channel message")
	}
}

func TestPerceive_DirectMessage(t *testing.T) {
	ag := newTestAgent()
	evt := makeDirectMessageEvent("hello", "user1")

	p := ag.Perceive(context.Background(), []event.Event{evt})
	if p == nil {
		t.Fatal("Perceive returned nil for DM event")
	}

	if !p.IsDM {
		t.Error("expected IsDM=true for DM event")
	}
	if !p.DirectlyAddressed {
		t.Error("DM should be directly addressed")
	}
}

func TestPerceive_MentionEvent(t *testing.T) {
	ag := newTestAgent()
	evt := makeMentionEvent("hey bot", "ch1", "user1")

	p := ag.Perceive(context.Background(), []event.Event{evt})
	if p == nil {
		t.Fatal("Perceive returned nil for mention event")
	}

	if !p.DirectlyAddressed {
		t.Error("mention should be directly addressed")
	}
}

func TestPerceive_EmptyBatch(t *testing.T) {
	ag := newTestAgent()

	p := ag.Perceive(context.Background(), nil)
	// Empty batch means no events to process — agent skips.
	// With nil channelSettings, the filter is skipped and we'd hit
	// an empty batch loop which yields zero-value Perception.
	// The important thing is it doesn't panic.
	_ = p
}

func TestPerceive_ContextGrows(t *testing.T) {
	ag := newTestAgent()
	before := ag.AgentContext().Len()

	evt := makeMessageEvent("test", "ch1", "user1")
	ag.Perceive(context.Background(), []event.Event{evt})

	after := ag.AgentContext().Len()
	if after <= before {
		t.Errorf("context length should grow after Perceive: before=%d after=%d", before, after)
	}
}
