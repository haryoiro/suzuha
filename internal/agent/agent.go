package agent

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/location"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// DefaultDrainWindow is the default delay after the last event before
// finalizing a batch. This allows closely-spaced messages to be grouped.
const DefaultDrainWindow = 3 * time.Second

// Agent is the main event loop that processes events, calls the LLM,
// executes tools, and sends responses.
type Agent struct {
	contexts  map[SourceKey]*Context
	compactMu map[SourceKey]*sync.Mutex
	sessions  map[SourceKey]Session
	llm       *llm.Client
	tools     *tool.Registry
	memory    memory.Store
	users     user.Store
	bus       *event.Bus
	consol    consolidator.Client
	db              *sql.DB // shared DB for channel activity tracking
	channelSettings *channelpkg.Store
	locationStore   *location.Store
	mediaStore      memory.MediaStore
	logger          *slog.Logger
	metrics         *observe.Metrics
	hooks  []PipelineHook
	tracer trace.Tracer // nil when tracing is disabled

	systemPrompt     string
	botID            string
	contextWindowPct float64
	drainWindow      time.Duration

	// ExpressionBroadcaster is called to broadcast expression changes to device/web clients.
	ExpressionBroadcaster func(expression int)

	lastEphemeralMu sync.RWMutex
	lastEphemeral   []llm.Message
}

// broadcastExpression sends an expression change if a broadcaster is configured.
func (a *Agent) broadcastExpression(expression int) {
	if a.ExpressionBroadcaster != nil {
		a.ExpressionBroadcaster(expression)
	}
}

// Config holds agent configuration.
type Config struct {
	SystemPrompt     string
	BotID            string
	ContextWindowPct float64       // trigger compaction at this ratio (e.g. 0.8)
	MaxContextTokens int
	DrainWindow      time.Duration // batch window; 0 = use DefaultDrainWindow, negative = non-blocking (tests)
}

// Perception is the output of the Perceive stage.
type Perception struct {
	LastMessage       llm.Message
	LastEvent         event.Event
	Channel           string
	IsDM              bool
	IsVoice           bool
	DirectlyAddressed bool
	SenderIsBot       bool
	TurnStartIdx      int
}

// Thought is the output of the Think stage.
type Thought struct {
	Ephemeral  []llm.Message
	Directive  string
	ListenMode bool
}

// New creates an Agent.
func New(
	cfg Config,
	llmClient *llm.Client,
	tools *tool.Registry,
	memStore memory.Store,
	userStore user.Store,
	bus *event.Bus,
	chatIface chat.Interface,
	consolClient consolidator.Client,
	db *sql.DB,
	channelSettings *channelpkg.Store,
	logger *slog.Logger,
	metrics *observe.Metrics,
) *Agent {
	dw := cfg.DrainWindow
	if dw == 0 {
		dw = DefaultDrainWindow
	}

	// Create contexts for each source key.
	contexts := map[SourceKey]*Context{
		SourceKeyDiscord: NewContext(cfg.MaxContextTokens),
		SourceKeyDevice:  NewContext(cfg.MaxContextTokens),
		SourceKeyWeb:     NewContext(cfg.MaxContextTokens),
	}

	// System prompt is stored separately — immune to compaction/truncation.
	for _, agentCtx := range contexts {
		agentCtx.SetSystemPrompt(cfg.SystemPrompt)
	}

	// Try to restore contexts from previous session.
	for key, agentCtx := range contexts {
		if saved := loadContextWith(db, logger, string(key)); len(saved) > 0 {
			// Backward compat: strip system prompt from messages if present.
			if saved[0].Role == "system" {
				saved = saved[1:]
			}
			agentCtx.ReplaceAll(saved)
			logger.Info("前の記憶を思い出した", "source_key", string(key), "messages", len(saved))
		}
	}

	// Create per-source compact mutexes.
	compactMu := map[SourceKey]*sync.Mutex{
		SourceKeyDiscord: {},
		SourceKeyDevice:  {},
		SourceKeyWeb:     {},
	}

	ag := &Agent{
		contexts:         contexts,
		compactMu:        compactMu,
		sessions:         make(map[SourceKey]Session),
		llm:              llmClient,
		tools:            tools,
		memory:           memStore,
		users:            userStore,
		bus:              bus,
		consol:           consolClient,
		db:               db,
		channelSettings:  channelSettings,
		logger:           logger,
		metrics:          metrics,
		systemPrompt:     cfg.SystemPrompt,
		botID:            cfg.BotID,
		contextWindowPct: cfg.ContextWindowPct,
		drainWindow:      dw,
	}

	// Create default sessions. These can be replaced via SetSession().
	ag.sessions[SourceKeyDiscord] = NewDiscordSession(
		contexts[SourceKeyDiscord],
		chatIface,
		nil, // voiceSpeaker set later
		channelSettings,
		dw,
		logger,
	)
	ag.sessions[SourceKeyDevice] = NewDeviceSession(
		contexts[SourceKeyDevice],
		nil, // deviceSpeaker set later
		logger,
	)
	ag.sessions[SourceKeyWeb] = NewWebSession(
		contexts[SourceKeyWeb],
		nil, // webSpeaker set later
		logger,
	)

	return ag
}

// AgentContext returns the agent's discord context for external use (e.g. tool callbacks).
func (a *Agent) AgentContext() *Context {
	return a.contexts[SourceKeyDiscord]
}

// AgentContextFor returns the context for the given source key.
func (a *Agent) AgentContextFor(key SourceKey) *Context {
	return a.contexts[key]
}

// SetBotID updates the bot's platform user ID at runtime.
func (a *Agent) SetBotID(id string) {
	a.botID = id
}

// BotID returns the bot's platform user ID.
func (a *Agent) BotID() string {
	return a.botID
}

// DeviceSpeaker is the interface for sending TTS to a physical device.
type DeviceSpeaker interface {
	SpeakText(ctx context.Context, text string) error
}

// SetSession registers a Session for the given source key.
func (a *Agent) SetSession(key SourceKey, sess Session) {
	a.sessions[key] = sess
	// Keep contexts map in sync: if the session provides a context,
	// use it as the canonical context for this source key.
	if sess.Context() != nil {
		a.contexts[key] = sess.Context()
	}
}

// GetSession returns the Session for the given source key, or nil if not set.
func (a *Agent) GetSession(key SourceKey) Session {
	return a.sessions[key]
}

// SetLocationStore sets the location store for GPS context injection.
func (a *Agent) SetLocationStore(s *location.Store) {
	a.locationStore = s
}

// SetMediaStore sets the media store for loading memory attachments.
func (a *Agent) SetMediaStore(s memory.MediaStore) {
	a.mediaStore = s
}

// SetTracer sets the OpenTelemetry tracer for pipeline and tool call tracing.
func (a *Agent) SetTracer(t trace.Tracer) {
	a.tracer = t
}

// LastEphemeral returns the most recently injected ephemeral messages.
func (a *Agent) LastEphemeral() []llm.Message {
	a.lastEphemeralMu.RLock()
	defer a.lastEphemeralMu.RUnlock()
	out := make([]llm.Message, len(a.lastEphemeral))
	copy(out, a.lastEphemeral)
	return out
}

// Run starts the agent event loop. Blocks until ctx is canceled.
// Events are routed to per-source buffered channels and processed by
// dedicated worker goroutines.
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	// Create per-source channels.
	channels := map[SourceKey]chan event.Event{
		SourceKeyDiscord: make(chan event.Event, 16),
		SourceKeyDevice:  make(chan event.Event, 16),
		SourceKeyWeb:     make(chan event.Event, 16),
	}

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
	sess := a.sessions[key]
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

			if a.metrics != nil {
				for _, e := range batch {
					a.metrics.EventsTotal.WithLabelValues(e.Source, e.Type).Inc()
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
					if a.metrics != nil {
						for _, e := range latest {
							a.metrics.EventsTotal.WithLabelValues(e.Source, e.Type).Inc()
						}
					}
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

// handleBatchWith orchestrates the 4-stage pipeline for a specific source:
// Perceive -> (compact check) -> Think -> Act -> Reflect.
// PipelineHooks are called after each stage for observability.
func (a *Agent) handleBatchWith(ctx context.Context, key SourceKey, batch []event.Event) error {
	sess := a.sessions[key]
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
	if a.metrics != nil {
		a.metrics.ContextWindowUsage.Set(ratio)
	}
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
		persistContextWith(ctx, a.db, agentCtx, a.logger, string(key))
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
		a.logger.Info("話した", "source_key", string(key), "length", len(text), "content", truncate(text, 200))
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
	if sess := a.sessions[key]; sess != nil {
		agentCtx = sess.Context()
		dc = sess.DirectiveConfig()
	} else {
		agentCtx = a.contexts[key]
		dc = a.directiveConfigFor(key)
	}

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
	for _, agentCtx := range a.contexts {
		agentCtx.SetSystemPrompt(newPrompt)
	}
	a.logger.Info("人格を読み直した", "length", len(newPrompt))
}

// ForceCompact triggers context compaction externally (e.g. from admin API).
func (a *Agent) ForceCompact(ctx context.Context) {
	a.compact(ctx)
}

// truncate shortens a string to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
