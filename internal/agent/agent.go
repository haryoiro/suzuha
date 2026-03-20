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
)

// DefaultDrainWindow is the default delay after the last event before
// finalizing a batch. This allows closely-spaced messages to be grouped.
const DefaultDrainWindow = 3 * time.Second

// Agent is the main event loop that processes events, calls the LLM,
// executes tools, and sends responses.
type Agent struct {
	contexts  map[SourceKey]*Context
	compactMu map[SourceKey]*sync.Mutex
	llm       *llm.Client
	tools     *tool.Registry
	memory    memory.Store
	users     user.Store
	bus       *event.Bus
	chat      chat.Interface
	consol    consolidator.Client
	db              *sql.DB // shared DB for channel activity tracking
	channelSettings *channelpkg.Store
	locationStore   *location.Store
	mediaStore      memory.MediaStore
	logger          *slog.Logger
	metrics         *observe.Metrics
	hooks           []PipelineHook
	voiceSpeaker    chat.VoiceSpeaker
	deviceSpeaker   DeviceSpeaker

	systemPrompt     string
	botID            string
	contextWindowPct float64
	drainWindow      time.Duration

	lastEphemeralMu sync.RWMutex
	lastEphemeral   []llm.Message
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
			logger.Info("DBからコンテキスト復元", "source_key", string(key), "messages", len(saved))
		}
	}

	// Create per-source compact mutexes.
	compactMu := map[SourceKey]*sync.Mutex{
		SourceKeyDiscord: {},
		SourceKeyDevice:  {},
	}

	return &Agent{
		contexts:         contexts,
		compactMu:        compactMu,
		llm:              llmClient,
		tools:            tools,
		memory:           memStore,
		users:            userStore,
		bus:              bus,
		chat:             chatIface,
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

// SetVoiceSpeaker sets the voice speaker for voice channel responses.
func (a *Agent) SetVoiceSpeaker(vs chat.VoiceSpeaker) {
	a.voiceSpeaker = vs
}

// SetDeviceSpeaker sets the device speaker for physical agent TTS responses.
func (a *Agent) SetDeviceSpeaker(ds DeviceSpeaker) {
	a.deviceSpeaker = ds
}

// SetLocationStore sets the location store for GPS context injection.
func (a *Agent) SetLocationStore(s *location.Store) {
	a.locationStore = s
}

// SetMediaStore sets the media store for loading memory attachments.
func (a *Agent) SetMediaStore(s memory.MediaStore) {
	a.mediaStore = s
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
	dc := a.directiveConfigFor(key)
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
				a.logger.Info("バッチ処理", "batch_size", len(batch), "source_key", string(key))
			}

			if err := a.handleBatchWith(ctx, key, batch); err != nil {
				a.logger.Error("イベント処理失敗", "error", err.Error(), "source_key", string(key))
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
						a.logger.Error("イベント処理失敗", "error", err.Error(), "source_key", string(key))
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
	agentCtx := a.contexts[key]
	dc := a.directiveConfigFor(key)

	// 1. Perceive: ingest events, resolve users, describe images.
	p := a.PerceiveWith(ctx, agentCtx, batch, dc)
	if p == nil {
		return nil
	}
	a.runHooks(func(h PipelineHook) error { return h.AfterPerceive(ctx, batch, p) })

	// 2. Compact context if needed.
	ratio := agentCtx.UsageRatio()
	if a.metrics != nil {
		a.metrics.ContextWindowUsage.Set(ratio)
	}
	a.logger.Debug("コンテキストウィンドウ", "usage_ratio", fmt.Sprintf("%.2f", ratio),
		"message_count", len(agentCtx.Messages()),
		"calibration", fmt.Sprintf("%.2f", agentCtx.TokenCalibration()),
		"source_key", string(key))
	if a.contextWindowPct > 0 && ratio > a.contextWindowPct {
		a.logger.Info("コンテキスト圧縮を開始", "ratio", fmt.Sprintf("%.2f", ratio), "source_key", string(key))
		a.compactAsyncFor(ctx, agentCtx, key)
	}

	// 3. Think: build ephemeral context and determine directive.
	t := a.ThinkWith(ctx, agentCtx, p, dc)
	a.runHooks(func(h PipelineHook) error { return h.AfterThink(ctx, p, t) })
	if t.ListenMode {
		persistContextWith(ctx, a.db, agentCtx, a.logger, string(key))
		return nil
	}

	// 4. Act: LLM completion, tool loop, get response text.
	text, err := a.ActWith(ctx, agentCtx, p, t)
	if err != nil {
		return err
	}
	a.runHooks(func(h PipelineHook) error { return h.AfterAct(ctx, p, t) })

	// 5. Route response to the appropriate output.
	if text != "" {
		a.routeResponse(ctx, key, p, text)
	}

	// 6. Reflect: log turn, persist context.
	a.ReflectWith(ctx, agentCtx, p, key)
	a.runHooks(func(h PipelineHook) error { return h.AfterReflect(ctx, p) })
	return nil
}

// routeResponse sends the response text to the appropriate output for the source.
func (a *Agent) routeResponse(ctx context.Context, key SourceKey, p *Perception, text string) {
	a.logger.Info("応答を送信",
		"channel", p.Channel,
		"length", len(text),
		"is_voice", p.IsVoice,
		"source_key", string(key),
		"content", truncate(text, 200))

	switch key {
	case SourceKeyDevice:
		if a.deviceSpeaker != nil {
			a.logger.Info("device: TTSで応答", "length", len(text))
			if err := a.deviceSpeaker.SpeakText(ctx, text); err != nil {
				a.logger.Warn("device: TTS送信失敗", "error", err)
			}
		}
	default:
		// Discord: check voice first, then text.
		if a.voiceSpeaker != nil && p.LastEvent.Message.GuildID != "" && a.voiceSpeaker.IsConnected(p.LastEvent.Message.GuildID) {
			guildID := p.LastEvent.Message.GuildID
			a.logger.Info("voice: 音声で応答", "guild", guildID, "length", len(text))
			if err := a.voiceSpeaker.SpeakText(ctx, guildID, text); err != nil {
				a.logger.Warn("voice: 音声送信失敗、テキストにフォールバック", "error", err)
				if err := a.chat.Send(ctx, p.Channel, text); err != nil {
					a.logger.Error("agent: 送信に失敗", "error", err)
				}
			}
		} else {
			if err := a.chat.Send(ctx, p.Channel, text); err != nil {
				a.logger.Error("agent: 送信に失敗", "error", err)
			}
		}
	}
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
	agentCtx := a.contexts[key]
	dc := a.directiveConfigFor(key)

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
					a.logger.Info("スキップ: 古いバッチを取り込み済み（応答なし）",
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
	a.logger.Info("システムプロンプト再読み込み", "length", len(newPrompt))
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
