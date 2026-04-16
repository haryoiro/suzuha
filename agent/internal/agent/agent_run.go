package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Run starts the agent event loop. Blocks until ctx is canceled.
// Events are routed to per-source buffered channels and processed by
// dedicated worker goroutines.
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	// Create per-source channels from registered sessions.
	a.mu.RLock()
	channels := make(map[SourceKey]chan event.Event, len(a.sessions))
	for key := range a.sessions {
		channels[key] = make(chan event.Event, 16)
	}
	a.mu.RUnlock()

	// Launch one worker per source key.
	var wg sync.WaitGroup
	for key, ch := range channels {
		wg.Add(1)
		go func(k SourceKey, c chan event.Event) {
			defer wg.Done()
			a.runWorker(ctx, k, c)
		}(key, ch)
	}

	// Dispatch: route events to per-source channels.
	for {
		select {
		case <-ctx.Done():
			// Close all channels so workers exit.
			for _, ch := range channels {
				close(ch)
			}
			wg.Wait()
			return ctx.Err()
		case evt := <-events:
			key := sourceKeyForEvent(evt.Source)
			if ch, ok := channels[key]; ok {
				ch <- evt
			} else {
				channels[SourceKeyDiscord] <- evt
			}
		}
	}
}

// runWorker processes events for a single source key.
func (a *Agent) runWorker(ctx context.Context, key SourceKey, ch <-chan event.Event) {
	a.mu.RLock()
	sess := a.sessions[key]
	a.mu.RUnlock()
	var dc DirectiveConfig
	if sess != nil {
		dc = sess.DirectiveConfig()
	} else {
		dc = a.directiveConfigFor(key)
	}
	drainWindow := dc.DrainWindow
	if key == SourceKeyDiscord {
		// Discord uses the agent-level drainWindow (which defaults or is configured).
		drainWindow = a.drainWindow
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			batch := []event.Event{evt}

			if drainWindow > 0 {
				// Timed drain: wait for additional events within the window.
				timer := time.NewTimer(drainWindow)
			drain:
				for {
					select {
					case e, ok := <-ch:
						if !ok {
							timer.Stop()
							break drain
						}
						batch = append(batch, e)
						timer.Reset(drainWindow)
					case <-timer.C:
						break drain
					case <-ctx.Done():
						timer.Stop()
						return
					}
				}
				timer.Stop()
			} else if drainWindow == 0 {
				// Non-blocking drain for zero drain window (device).
			drainFast:
				for {
					select {
					case e, ok := <-ch:
						if !ok {
							break drainFast
						}
						batch = append(batch, e)
					default:
						break drainFast
					}
				}
			} else {
				// Negative drain window (tests): non-blocking drain.
			drainTest:
				for {
					select {
					case e, ok := <-ch:
						if !ok {
							break drainTest
						}
						batch = append(batch, e)
					default:
						break drainTest
					}
				}
			}

			if len(batch) > 1 {
				a.logger.Info("まとめて処理する", "batch_size", len(batch), "source_key", string(key))
			}

			if err := a.handleBatchWith(ctx, key, batch); err != nil {
				a.logger.Error("処理に失敗した", "error", err.Error(), "source_key", string(key))
			}

			// After processing, catch up on any events that arrived during
			// handleBatch (only for sources that don't skip catch-up).
			if !dc.SkipCatchUpStale {
				if latest := a.catchUpStaleFor(ctx, key, ch, drainWindow); len(latest) > 0 {
					if err := a.handleBatchWith(ctx, key, latest); err != nil {
						a.logger.Error("処理に失敗した", "error", err.Error(), "source_key", string(key))
					}
				}
			}
		}
	}
}

// directiveConfigFor returns the DirectiveConfig for a given source key.
func (a *Agent) directiveConfigFor(key SourceKey) DirectiveConfig {
	switch key {
	case SourceKeyDevice:
		return deviceDirectiveConfig()
	default:
		return discordDirectiveConfig(a.drainWindow)
	}
}

// handleBatch is the backward-compatible wrapper using the discord source key.
func (a *Agent) handleBatch(ctx context.Context, batch []event.Event) error {
	return a.handleBatchWith(ctx, SourceKeyDiscord, batch)
}

// HandleBatch はイベントバッチを指定ソースのパイプラインで処理する。
// ベンチマークやテストで外部から呼び出すための public メソッド。
func (a *Agent) HandleBatch(ctx context.Context, batch []event.Event) error {
	if len(batch) == 0 {
		return nil
	}
	key := sourceKeyForEvent(batch[0].Source)
	return a.handleBatchWith(ctx, key, batch)
}

// handleBatchWith orchestrates the 4-stage pipeline for a specific source:
// Perceive -> (compact check) -> Think -> Act -> Reflect.
// PipelineHooks are called after each stage for observability.
func (a *Agent) handleBatchWith(ctx context.Context, key SourceKey, batch []event.Event) error {
	a.mu.RLock()
	sess := a.sessions[key]
	a.mu.RUnlock()
	if sess == nil {
		return fmt.Errorf("no session for source %s", key)
	}
	agentCtx := sess.Context()
	dc := sess.DirectiveConfig()

	// Start turn-level tracing spans.
	hookCtx := a.beginPipelineHooks(ctx)
	defer a.endPipelineHooks(hookCtx)

	// 1. Perceive: ingest events, resolve users, describe images.
	p := a.PerceiveWith(ctx, agentCtx, batch, dc)
	if p == nil {
		return nil
	}
	a.runHooksWithCtx(hookCtx, func(c context.Context, h PipelineHook) error { return h.AfterPerceive(c, batch, p) })

	// Set turn context for response routing.
	sess.BeginTurn(p)

	// 2. Compact context if needed.
	ratio := agentCtx.UsageRatio()
	a.logger.Debug("記憶の状態", "usage_ratio", fmt.Sprintf("%.2f", ratio),
		"message_count", len(agentCtx.Messages()),
		"calibration", fmt.Sprintf("%.2f", agentCtx.TokenCalibration()),
		"source_key", string(key))
	if a.contextWindowPct > 0 && ratio > a.contextWindowPct {
		a.logger.Info("記憶を整理する", "ratio", fmt.Sprintf("%.2f", ratio), "source_key", string(key))
		a.compactAsyncFor(ctx, agentCtx, key)
	}

	// Show "thinking" expression on device/web clients while processing.
	if key == SourceKeyDevice || key == SourceKeyWeb {
		a.broadcastExpression(6) // thinking
	}

	// 3. Think: build ephemeral context and determine directive.
	t := a.ThinkWith(ctx, agentCtx, p, dc)
	a.runHooksWithCtx(hookCtx, func(c context.Context, h PipelineHook) error { return h.AfterThink(c, p, t) })
	if t.ListenMode {
		a.persistContext(ctx, agentCtx, key)
		return nil
	}

	// 4. Act: LLM completion, tool loop, get response text.
	// Use hookCtx so LLM/tool spans are children of the pipeline.turn trace.
	text, err := a.ActWith(hookCtx, agentCtx, sess, p, t)
	if err != nil {
		return err
	}
	// Record bot response on the root turn span for Langfuse output.
	if rootSpan := trace.SpanFromContext(hookCtx); rootSpan.SpanContext().IsValid() && text != "" {
		rootSpan.SetAttributes(attribute.String("suzuha.output", text))
	}
	a.runHooksWithCtx(hookCtx, func(c context.Context, h PipelineHook) error { return h.AfterAct(c, p, t) })

	// 5. Route response through the session.
	if text != "" {
		a.logger.Info("話した", "source_key", string(key), "length", len(text), "content", textutil.TruncateRunes(text, 200))
		if err := sess.Respond(ctx, text); err != nil {
			a.logger.Error("返事の送信に失敗", "error", err)
		}
	}

	// Revert to neutral expression after responding.
	if key == SourceKeyDevice || key == SourceKeyWeb {
		a.broadcastExpression(0) // neutral
	}

	// 6. Reflect: log turn, persist context.
	a.ReflectWith(ctx, agentCtx, p, key)
	a.runHooksWithCtx(hookCtx, func(c context.Context, h PipelineHook) error { return h.AfterReflect(c, p) })
	return nil
}

// catchUpStale is the backward-compatible wrapper.
func (a *Agent) catchUpStale(ctx context.Context, events <-chan event.Event) []event.Event {
	return a.catchUpStaleFor(ctx, SourceKeyDiscord, events, a.drainWindow)
}

// catchUpStaleFor drains queued events for a source, ingests them all into context
// (Perceive-only), and returns only the final batch for full pipeline
// processing. This keeps conversation history complete while skipping
// LLM responses to stale messages — critical for voice tempo.
// Returns nil if no events were queued.
func (a *Agent) catchUpStaleFor(ctx context.Context, key SourceKey, events <-chan event.Event, drainWindow time.Duration) []event.Event {
	var agentCtx *Context
	var dc DirectiveConfig
	a.mu.RLock()
	if sess := a.sessions[key]; sess != nil {
		agentCtx = sess.Context()
		dc = sess.DirectiveConfig()
	} else {
		agentCtx = a.contexts[key]
		dc = a.directiveConfigFor(key)
	}
	a.mu.RUnlock()

	var latest []event.Event
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return latest
			}
			// If we had a previous batch, perceive-only (ingest without responding).
			if len(latest) > 0 {
				p := a.PerceiveWith(ctx, agentCtx, latest, dc)
				if p != nil {
					a.logger.Info("溜まってた話を聞いた（返事はしない）",
						"batch_size", len(latest), "channel", p.Channel, "source_key", string(key))
					a.ReflectWith(ctx, agentCtx, p, key)
				}
			}
			// Start a new "latest" batch and drain within the window.
			latest = []event.Event{evt}
			if drainWindow > 0 {
				timer := time.NewTimer(drainWindow)
			drainCatchUp:
				for {
					select {
					case e, ok := <-events:
						if !ok {
							timer.Stop()
							break drainCatchUp
						}
						latest = append(latest, e)
						timer.Reset(drainWindow)
					case <-timer.C:
						break drainCatchUp
					case <-ctx.Done():
						timer.Stop()
						return nil
					}
				}
				timer.Stop()
			}
		default:
			// No more events queued.
			return latest
		}
	}
}

// ReloadPrompt updates the system prompt across all contexts.
func (a *Agent) ReloadPrompt(newPrompt string) {
	a.systemPrompt = newPrompt
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, agentCtx := range a.contexts {
		agentCtx.SetSystemPrompt(newPrompt)
	}
	a.logger.Info("人格を読み直した", "length", len(newPrompt))
}

// ForceCompact triggers context compaction externally (e.g. from admin API).
func (a *Agent) ForceCompact(ctx context.Context) {
	a.compact(ctx)
}
