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
	ctx     *Context
	llm     *llm.Client
	tools   *tool.Registry
	memory  memory.Store
	users   user.Store
	bus     *event.Bus
	chat    chat.Interface
	consol  consolidator.Client
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

	compactMu sync.Mutex // guards background compaction (at most one at a time)

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

	agentCtx := NewContext(cfg.MaxContextTokens)

	// System prompt is stored separately — immune to compaction/truncation.
	agentCtx.SetSystemPrompt(cfg.SystemPrompt)

	// Try to restore context from previous session.
	if saved := loadContext(db, logger); len(saved) > 0 {
		// Backward compat: strip system prompt from messages if present.
		if saved[0].Role == "system" {
			saved = saved[1:]
		}
		agentCtx.ReplaceAll(saved)
		logger.Info("DBからコンテキスト復元", "messages", len(saved))
	}

	return &Agent{
		ctx:              agentCtx,
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

// AgentContext returns the agent's context for external use (e.g. tool callbacks).
func (a *Agent) AgentContext() *Context {
	return a.ctx
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
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-events:
			batch := []event.Event{evt}
			if a.drainWindow > 0 {
				// Timed drain: wait for additional events within the window.
				timer := time.NewTimer(a.drainWindow)
			drain:
				for {
					select {
					case e := <-events:
						batch = append(batch, e)
						timer.Reset(a.drainWindow)
					case <-timer.C:
						break drain
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					}
				}
				timer.Stop()
			} else {
				// Non-blocking drain (tests / drainWindow < 0).
			drainFast:
				for {
					select {
					case e := <-events:
						batch = append(batch, e)
					default:
						break drainFast
					}
				}
			}

			if a.metrics != nil {
				for _, e := range batch {
					a.metrics.EventsTotal.WithLabelValues(e.Source, e.Type).Inc()
				}
			}

			if len(batch) > 1 {
				a.logger.Info("バッチ処理", "batch_size", len(batch))
			}

			if err := a.handleBatch(ctx, batch); err != nil {
				a.logger.Error("イベント処理失敗", "error", err.Error())
			}

			// After processing, catch up on any events that arrived during
			// handleBatch. Stale batches are ingested into context (Perceive
			// + Reflect only); only the latest batch gets full pipeline.
			if latest := a.catchUpStale(ctx, events); len(latest) > 0 {
				if a.metrics != nil {
					for _, e := range latest {
						a.metrics.EventsTotal.WithLabelValues(e.Source, e.Type).Inc()
					}
				}
				if err := a.handleBatch(ctx, latest); err != nil {
					a.logger.Error("イベント処理失敗", "error", err.Error())
				}
			}
		}
	}
}

// handleBatch orchestrates the 4-stage pipeline:
// Perceive → (compact check) → Think → Act → Reflect.
// PipelineHooks are called after each stage for observability.
func (a *Agent) handleBatch(ctx context.Context, batch []event.Event) error {
	// 1. Perceive: ingest events, resolve users, describe images.
	p := a.Perceive(ctx, batch)
	if p == nil {
		return nil
	}
	a.runHooks(func(h PipelineHook) error { return h.AfterPerceive(ctx, batch, p) })

	// 2. Compact context if needed.
	ratio := a.ctx.UsageRatio()
	if a.metrics != nil {
		a.metrics.ContextWindowUsage.Set(ratio)
	}
	a.logger.Debug("コンテキストウィンドウ", "usage_ratio", fmt.Sprintf("%.2f", ratio),
		"message_count", len(a.ctx.Messages()),
		"calibration", fmt.Sprintf("%.2f", a.ctx.TokenCalibration()))
	if a.contextWindowPct > 0 && ratio > a.contextWindowPct {
		a.logger.Info("コンテキスト圧縮を開始", "ratio", fmt.Sprintf("%.2f", ratio))
		a.compactAsync(ctx)
	}

	// 3. Think: build ephemeral context and determine directive.
	t := a.Think(ctx, p)
	a.runHooks(func(h PipelineHook) error { return h.AfterThink(ctx, p, t) })
	if t.ListenMode {
		persistContext(ctx, a.db, a.ctx, a.logger)
		return nil
	}

	// 4. Act: LLM completion, tool loop, send response.
	if err := a.Act(ctx, p, t); err != nil {
		return err
	}
	a.runHooks(func(h PipelineHook) error { return h.AfterAct(ctx, p, t) })

	// 5. Reflect: log turn, persist context.
	a.Reflect(ctx, p)
	a.runHooks(func(h PipelineHook) error { return h.AfterReflect(ctx, p) })
	return nil
}

// catchUpStale drains queued events, ingests them all into context
// (Perceive-only), and returns only the final batch for full pipeline
// processing. This keeps conversation history complete while skipping
// LLM responses to stale messages — critical for voice tempo.
// Returns nil if no events were queued.
func (a *Agent) catchUpStale(ctx context.Context, events <-chan event.Event) []event.Event {
	var latest []event.Event
	for {
		select {
		case evt := <-events:
			// If we had a previous batch, perceive-only (ingest without responding).
			if len(latest) > 0 {
				p := a.Perceive(ctx, latest)
				if p != nil {
					a.logger.Info("スキップ: 古いバッチを取り込み済み（応答なし）",
						"batch_size", len(latest), "channel", p.Channel)
					a.Reflect(ctx, p)
				}
			}
			// Start a new "latest" batch and drain within the window.
			latest = []event.Event{evt}
			if a.drainWindow > 0 {
				timer := time.NewTimer(a.drainWindow)
			drainCatchUp:
				for {
					select {
					case e := <-events:
						latest = append(latest, e)
						timer.Reset(a.drainWindow)
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

// ReloadPrompt updates the system prompt.
func (a *Agent) ReloadPrompt(newPrompt string) {
	a.systemPrompt = newPrompt
	a.ctx.SetSystemPrompt(newPrompt)
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
