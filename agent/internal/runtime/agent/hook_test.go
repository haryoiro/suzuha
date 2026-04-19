package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/haryoiro/suzuha/internal/runtime/event"
)

// recordingHook records which pipeline stages were called.
type recordingHook struct {
	mu     sync.Mutex
	stages []string
}

func (h *recordingHook) AfterPerceive(_ context.Context, _ []event.Event, _ *Perception) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stages = append(h.stages, "perceive")
	return nil
}

func (h *recordingHook) AfterThink(_ context.Context, _ *Perception, _ *Thought) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stages = append(h.stages, "think")
	return nil
}

func (h *recordingHook) AfterAct(_ context.Context, _ *Perception, _ *Thought) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stages = append(h.stages, "act")
	return nil
}

func (h *recordingHook) AfterReflect(_ context.Context, _ *Perception) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stages = append(h.stages, "reflect")
	return nil
}

var _ PipelineHook = (*recordingHook)(nil)

// errorHook always returns an error.
type errorHook struct {
	called bool
}

func (h *errorHook) AfterPerceive(_ context.Context, _ []event.Event, _ *Perception) error {
	h.called = true
	return errors.New("フックエラー")
}
func (h *errorHook) AfterThink(_ context.Context, _ *Perception, _ *Thought) error { return nil }
func (h *errorHook) AfterAct(_ context.Context, _ *Perception, _ *Thought) error   { return nil }
func (h *errorHook) AfterReflect(_ context.Context, _ *Perception) error           { return nil }

func TestHooks(t *testing.T) {
	t.Run("AfterPerceive is called", func(t *testing.T) {
		hook := &recordingHook{}
		ag := newTestAgent()
		ag.AddHook(hook)

		evt := makeMessageEvent("hello", "ch1", "user1")
		p := ag.Perceive(context.Background(), []event.Event{evt})
		if p == nil {
			t.Fatal("Perceive returned nil")
		}
		ag.runHooks(func(h PipelineHook) error { return h.AfterPerceive(context.Background(), []event.Event{evt}, p) })

		if len(hook.stages) != 1 || hook.stages[0] != "perceive" {
			t.Errorf("expected [perceive], got %v", hook.stages)
		}
	})

	t.Run("multiple hooks are called", func(t *testing.T) {
		hook1 := &recordingHook{}
		hook2 := &recordingHook{}
		ag := newTestAgent()
		ag.AddHook(hook1)
		ag.AddHook(hook2)

		evt := makeMessageEvent("test", "ch1", "user1")
		p := ag.Perceive(context.Background(), []event.Event{evt})
		ag.runHooks(func(h PipelineHook) error { return h.AfterPerceive(context.Background(), []event.Event{evt}, p) })

		if len(hook1.stages) != 1 {
			t.Errorf("hook1 should have 1 stage, got %d", len(hook1.stages))
		}
		if len(hook2.stages) != 1 {
			t.Errorf("hook2 should have 1 stage, got %d", len(hook2.stages))
		}
	})

	t.Run("error does not stop processing", func(t *testing.T) {
		errHook := &errorHook{}
		recHook := &recordingHook{}
		ag := newTestAgent()
		ag.AddHook(errHook)
		ag.AddHook(recHook)

		evt := makeMessageEvent("test", "ch1", "user1")
		p := ag.Perceive(context.Background(), []event.Event{evt})
		ag.runHooks(func(h PipelineHook) error { return h.AfterPerceive(context.Background(), []event.Event{evt}, p) })

		if !errHook.called {
			t.Error("error hook should have been called")
		}
		if len(recHook.stages) != 1 {
			t.Errorf("recording hook should have been called despite error in first hook, got %d stages", len(recHook.stages))
		}
	})
}
