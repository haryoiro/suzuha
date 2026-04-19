package agent

import (
	"context"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/runtime/event"
)

func TestTimedDrain(t *testing.T) {
	t.Run("batches close events", func(t *testing.T) {
		bus := event.NewBus(16)

		ag := newTestAgent(func(a *Agent) {
			a.drainWindow = 200 * time.Millisecond
		})
		ag.bus = bus

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)

		events := bus.Subscribe()

		go func() {
			bus.Publish(makeMessageEvent("msg1", "ch1", "user1"))
			time.Sleep(50 * time.Millisecond)
			bus.Publish(makeMessageEvent("msg2", "ch1", "user1"))
			time.Sleep(50 * time.Millisecond)
			bus.Publish(makeMessageEvent("msg3", "ch1", "user1"))
		}()

		var batch []event.Event
		select {
		case evt := <-events:
			batch = append(batch, evt)
		case <-ctx.Done():
			t.Fatal("timed out waiting for first event")
		}

		timer := time.NewTimer(ag.drainWindow)
		t.Cleanup(func() { timer.Stop() })
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
	})
}

func TestDrainWindow(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Agent)
		wantCheck func(*testing.T, *Agent)
	}{
		{
			"negative window",
			func(a *Agent) { a.drainWindow = -1 },
			func(t *testing.T, ag *Agent) {
				t.Helper()
				if ag.drainWindow >= 0 {
					t.Errorf("expected negative drainWindow, got %v", ag.drainWindow)
				}
			},
		},
		{
			"default window",
			nil,
			func(t *testing.T, ag *Agent) {
				t.Helper()
				if ag.drainWindow != DefaultDrainWindow {
					t.Errorf("expected default drain window %v, got %v", DefaultDrainWindow, ag.drainWindow)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ag *Agent
			if tt.configure != nil {
				ag = newTestAgent(tt.configure)
			} else {
				ag = newTestAgent()
			}
			tt.wantCheck(t, ag)
		})
	}
}
