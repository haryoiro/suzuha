package agent

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	acq "github.com/haryoiro/suzuha/internal/memento/acquirer"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/agent/prompt"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/feature/location"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// DefaultDrainWindow is the default delay after the last event before
// finalizing a batch. This allows closely-spaced messages to be grouped.
const DefaultDrainWindow = 3 * time.Second

// acquirer は agent が必要とするメモリ獲得機能を定義する (consumer-side interface)。
type acquirer interface {
	Acquire(ctx context.Context, req *acq.AcquireRequest) (*acq.AcquireResult, error)
}

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
	acquirer  acquirer
	db              *sql.DB // shared DB for channel activity tracking
	channelSettings *channelpkg.Store
	locationStore   *location.Store
	mediaStore      memory.MediaStore
	videoMeta       transcript.MetadataFetcher // nil if video feature not configured
	logger          *slog.Logger
	contextProviders []prompt.Provider
	hooks            []PipelineHook
	tracer           trace.Tracer

	systemPrompt     string
	botID            string
	contextWindowPct float64
	maxContextTokens int
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

type Thought struct {
	Background []llm.Message // 会話の前に置く前提知識（記憶・プロフ・日記）
	Foreground []llm.Message // 会話の後に置く状況・指示（self-prompt, home旗）
	Directive  string
	ListenMode bool
}

func (t *Thought) BuildMessages(systemPrompt string, conversation []llm.Message) []llm.Message {
	now := jtime.Now()
	var msgs []llm.Message
	if systemPrompt != "" {
		msgs = append(msgs, llm.Message{
			Role:    "system",
			Content: systemPrompt + fmt.Sprintf("\n\n[現在時刻: %s]", now.Format("2006-01-02 15:04:05 (Mon)")),
		})
	}
	msgs = append(msgs, t.Background...)
	msgs = append(msgs, conversation...)
	msgs = append(msgs, t.Foreground...)
	if t.Directive != "" {
		msgs = append(msgs, llm.Message{Role: "system", Content: t.Directive, Timestamp: now})
	}
	return msgs
}

// New creates an Agent.
func New(
	cfg Config,
	registrations []SourceRegistration,
	llmClient *llm.Client,
	tools *tool.Registry,
	memStore memory.Store,
	userStore user.Store,
	bus *event.Bus,
	acq acquirer,
	db *sql.DB,
	channelSettings *channelpkg.Store,
	logger *slog.Logger,
) *Agent {
	dw := cfg.DrainWindow
	if dw == 0 {
		dw = DefaultDrainWindow
	}

	contexts := make(map[SourceKey]*Context, len(registrations))
	compactMu := make(map[SourceKey]*sync.Mutex, len(registrations))
	sessions := make(map[SourceKey]Session, len(registrations))

	for _, reg := range registrations {
		agentCtx := NewContext(cfg.MaxContextTokens)
		agentCtx.SetSystemPrompt(cfg.SystemPrompt)

		persistKey := reg.PersistKey
		if persistKey == "" {
			persistKey = string(reg.Key)
		}

		// Try to restore context from previous session.
		if saved := loadContextWith(db, logger, persistKey); len(saved) > 0 {
			if saved[0].Role == "system" {
				saved = saved[1:]
			}
			agentCtx.ReplaceAll(saved)
			logger.Info("前の記憶を思い出した", "source_key", string(reg.Key), "messages", len(saved))
		}

		contexts[reg.Key] = agentCtx
		compactMu[reg.Key] = &sync.Mutex{}
		sessions[reg.Key] = reg.NewSession(agentCtx)
	}

	return &Agent{
		contexts:         contexts,
		compactMu:        compactMu,
		sessions:         sessions,
		llm:              llmClient,
		tools:            tools,
		memory:           memStore,
		users:            userStore,
		bus:              bus,
		acquirer:         acq,
		db:               db,
		channelSettings:  channelSettings,
		logger:           logger,
		systemPrompt:     cfg.SystemPrompt,
		botID:            cfg.BotID,
		contextWindowPct: cfg.ContextWindowPct,
		drainWindow:      dw,
		maxContextTokens: cfg.MaxContextTokens,
		contextProviders: buildProviders(memStore, db, userStore, cfg.BotID, logger),
	}
}

// AgentContext returns the agent's discord context for external use (e.g. tool callbacks).
func (a *Agent) AgentContext() *Context {
	return a.contexts[SourceKeyDiscord]
}

// AgentContextFor returns the context for the given source key.
// If no context exists yet, a new one is created and stored.
func (a *Agent) AgentContextFor(key SourceKey) *Context {
	if ctx, ok := a.contexts[key]; ok {
		return ctx
	}
	ctx := NewContext(a.maxContextTokens)
	ctx.SetSystemPrompt(a.systemPrompt)
	a.contexts[key] = ctx
	return ctx
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
	if sess.Context() != nil {
		a.contexts[key] = sess.Context()
	}
	// Ensure compactMu exists for dynamically added sources.
	if _, ok := a.compactMu[key]; !ok {
		a.compactMu[key] = &sync.Mutex{}
	}
}

// GetSession returns the Session for the given source key, or nil if not set.
func (a *Agent) GetSession(key SourceKey) Session {
	return a.sessions[key]
}

func (a *Agent) SetLocationStore(s *location.Store) {
	a.locationStore = s
	for _, p := range a.contextProviders {
		if lp, ok := p.(*prompt.LocationProvider); ok {
			lp.Store = s
		}
	}
}

func (a *Agent) SetVideoMeta(m transcript.MetadataFetcher) {
	a.videoMeta = m
}

func (a *Agent) SetMediaStore(s memory.MediaStore) {
	a.mediaStore = s
	for _, p := range a.contextProviders {
		if mp, ok := p.(*prompt.MemoryProvider); ok {
			mp.Media = s
		}
	}
}

func (a *Agent) SetTracer(t trace.Tracer) {
	a.tracer = t
}

func buildProviders(
	memStore memory.Store,
	db *sql.DB,
	userStore user.Store,
	botID string,
	logger *slog.Logger,
) []prompt.Provider {
	return []prompt.Provider{
		&prompt.DiaryProvider{DB: db, Logger: logger},
		&prompt.MemoryProvider{Memory: memStore, Logger: logger},
		&prompt.LocationProvider{},
		&prompt.ProfileProvider{Users: userStore, Memory: memStore, BotID: botID, Logger: logger},
		&prompt.ChannelProvider{},
		prompt.SelfPromptProvider{},
	}
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

	// Create per-source channels from registered sessions.
	channels := make(map[SourceKey]chan event.Event, len(a.sessions))
	for key := range a.sessions {
		channels[key] = make(chan event.Event, 16)
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

