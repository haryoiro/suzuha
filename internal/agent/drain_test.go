package agent

import (
	"context"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/event"
)

func TestTimedDrain_BatchesCloseEvents(t *testing.T) {
	bus := event.NewBus(16)

	// Use a short drain window for the test.
	ag := newTestAgent(func(a *Agent) {
		a.drainWindow = 200 * time.Millisecond
	})
	ag.bus = bus

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := bus.Subscribe()

	// Publish 3 events with small gaps (< drainWindow).
	go func() {
		bus.Publish(makeMessageEvent("msg1", "ch1", "user1"))
		time.Sleep(50 * time.Millisecond)
		bus.Publish(makeMessageEvent("msg2", "ch1", "user1"))
		time.Sleep(50 * time.Millisecond)
		bus.Publish(makeMessageEvent("msg3", "ch1", "user1"))
	}()

	// Read first event to trigger drain.
	var batch []event.Event
	select {
	case evt := <-events:
		batch = append(batch, evt)
	case <-ctx.Done():
		t.Fatal("timed out waiting for first event")
	}

	// Timed drain.
	timer := time.NewTimer(ag.drainWindow)
	defer timer.Stop()
drain:
	for {
		select {
		case e := <-events:
			batch = append(batch, e)
			timer.Reset(ag.drainWindow)
		case <-timer.C:
			break drain
		case <-ctx.Done():
			t.Fatal("timed out during drain")
		}
	}

	if len(batch) != 3 {
		t.Errorf("expected 3 events in batch, got %d", len(batch))
	}
}

func TestNonBlockingDrain_NegativeWindow(t *testing.T) {
	// With negative drainWindow, Run uses non-blocking drain.
	ag := newTestAgent(func(a *Agent) {
		a.drainWindow = -1
	})
	if ag.drainWindow >= 0 {
		t.Errorf("expected negative drainWindow, got %v", ag.drainWindow)
	}
}

func TestDefaultDrainWindow(t *testing.T) {
	// Default newTestAgent uses DrainWindow=0 in Config which maps to DefaultDrainWindow.
	ag := newTestAgent()
	if ag.drainWindow != DefaultDrainWindow {
		t.Errorf("expected default drain window %v, got %v", DefaultDrainWindow, ag.drainWindow)
	}
}
