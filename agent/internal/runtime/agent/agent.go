package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/lib/llmtoken"
	portconv "github.com/haryoiro/suzuha/internal/port/conversation"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	"github.com/haryoiro/suzuha/internal/port/user"
	"github.com/haryoiro/suzuha/internal/runtime/agent/prompt"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	toolreg "github.com/haryoiro/suzuha/internal/runtime/toolregistry"
	"go.opentelemetry.io/otel/trace"
)

// DefaultDrainWindow is the default delay after the last event before
// finalizing a batch. This allows closely-spaced messages to be grouped.
const DefaultDrainWindow = 3 * time.Second

// acquirer は agent が必要とするメモリ獲得機能を定義する (consumer-side interface)。
type acquirer interface {
	Acquire(ctx context.Context, req *portmem.AcquireRequest) (*portmem.AcquireResult, error)
}

// VideoInfo は動画のメタデータを保持する。
type VideoInfo struct {
	Title    string
	Duration float64
}

// VideoMetadataFetcher は動画 URL のメタデータを取得する (consumer-side interface)。
type VideoMetadataFetcher interface {
	Supports(url string) bool
	FetchMetadata(ctx context.Context, url string) (VideoInfo, error)
}

// TweetPreview はツイートのプレビュー情報を保持する。
type TweetPreview struct {
	AuthorID string
	Text     string
}

// TweetFetcher はツイートを取得する (consumer-side interface)。
type TweetFetcher interface {
	Supports(url string) bool
	Fetch(ctx context.Context, url string) (*TweetPreview, error)
}

// conversationStore は会話ログとコンテキストスナップショットの永続化を担う (consumer-side interface)。
type conversationStore interface {
	LogTurn(ctx context.Context, entry portconv.TurnEntry) error
	TrackActivity(ctx context.Context, channelID string, at time.Time) error
	SaveSnapshot(ctx context.Context, sourceKey string, messages []message.Message) error
	LoadSnapshot(ctx context.Context, sourceKey string) ([]message.Message, error)
	DeleteChannel(ctx context.Context, channelID string) error
}

// Agent is the main event loop that processes events, calls the LLM,
// executes tools, and sends responses.
type Agent struct {
	contexts         map[SourceKey]*Context
	compactMu        map[SourceKey]*sync.Mutex
	sessions         map[SourceKey]Session
	llm              portllm.Client
	tools            *toolreg.Registry
	memory           portmem.Memory
	users            user.Store
	bus              *event.Bus
	acquirer         acquirer
	convStore        conversationStore
	channelSettings  portconv.SettingsStore
	mediaStore       portmem.Media
	videoMeta        VideoMetadataFetcher
	tweetFetcher     TweetFetcher
	videoURLExtract  func(string) []string
	tweetURLExtract  func(string) []string
	logger           *slog.Logger
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
	lastBackground  []message.Message
	lastForeground  []message.Message

	// lastResponseMu protects lastResponse.
	lastResponseMu sync.Mutex
	// lastResponse はチャンネルごとの直前の返答テキストを保持する。
	// 同一チャンネルへの連続重複返答を防ぐために使用。
	lastResponse map[string]string
}

// isDuplicateResponse は直前の返答と類似しているかを判定し、未判定なら返答を記録する。
// 類似判定は isSimilarText (Levenshtein 0.85 以上) を使う。
func (a *Agent) isDuplicateResponse(channel, text string) bool {
	if channel == "" {
		return false
	}
	a.lastResponseMu.Lock()
	defer a.lastResponseMu.Unlock()
	if a.lastResponse == nil {
		a.lastResponse = make(map[string]string)
	}
	prev := a.lastResponse[channel]
	if prev != "" && isSimilarText(prev, text) {
		return true
	}
	a.lastResponse[channel] = text
	return false
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
	ContextWindowPct float64 // trigger compaction at this ratio (e.g. 0.8)
	MaxContextTokens int
	DrainWindow      time.Duration // batch window; 0 = use DefaultDrainWindow, negative = non-blocking (tests)
}

// Perception is the output of the Perceive stage.
type Perception struct {
	LastMessage       message.Message
	LastEvent         event.Event
	Channel           string
	IsDM              bool
	IsVoice           bool
	DirectlyAddressed bool
	SenderIsBot       bool
	TurnStartIdx      int
}

// Thought は Think ステージの出力で、LLM に渡す補助コンテキストを保持する。
type Thought struct {
	Background []message.Message // 会話の前に置く前提知識（記憶・プロフ・日記）
	Foreground []message.Message // 会話の後に置く状況・指示（self-prompt, home旗）
	Directive  string
	ListenMode bool
}

// BuildMessages はシステムプロンプトと会話履歴を組み合わせて LLM に渡すメッセージ列を構築する。
func (t *Thought) BuildMessages(systemPrompt string, conversation []message.Message) []message.Message {
	now := jtime.Now()
	var msgs []message.Message
	if systemPrompt != "" {
		msgs = append(msgs, message.Message{
			Role:    "system",
			Content: systemPrompt + fmt.Sprintf("\n\n[現在時刻: %s]", now.Format("2006-01-02 15:04:05 (Mon)")),
		})
	}
	msgs = append(msgs, t.Background...)
	msgs = append(msgs, conversation...)
	msgs = append(msgs, t.Foreground...)
	if t.Directive != "" {
		msgs = append(msgs, message.Message{Role: "system", Content: t.Directive, Timestamp: now})
	}
	return msgs
}

// New creates an Agent.
func New(
	cfg Config,
	registrations []SourceRegistration,
	llmClient portllm.Client,
	tools *toolreg.Registry,
	memStore portmem.Memory,
	userStore user.Store,
	bus *event.Bus,
	acq acquirer,
	convStore conversationStore,
	providers []prompt.Provider,
	channelSettings portconv.SettingsStore,
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
		if convStore != nil {
			if saved, err := convStore.LoadSnapshot(context.Background(), persistKey); err == nil && len(saved) > 0 {
				if saved[0].Role == "system" {
					saved = saved[1:]
				}
				agentCtx.ReplaceAll(saved)
				logger.Info("前の記憶を思い出した", "source_key", string(reg.Key), "messages", len(saved))
			}
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
		convStore:        convStore,
		channelSettings:  channelSettings,
		logger:           logger,
		systemPrompt:     cfg.SystemPrompt,
		botID:            cfg.BotID,
		contextWindowPct: cfg.ContextWindowPct,
		drainWindow:      dw,
		maxContextTokens: cfg.MaxContextTokens,
		contextProviders: providers,
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

// SetVideoMeta は動画メタデータフェッチャーと URL 抽出関数を設定する。
func (a *Agent) SetVideoMeta(m VideoMetadataFetcher, extractURLs func(string) []string) {
	a.videoMeta = m
	a.videoURLExtract = extractURLs
}

// SetTweetFetcher はツイートフェッチャーと URL 抽出関数を設定する。
func (a *Agent) SetTweetFetcher(f TweetFetcher, extractURLs func(string) []string) {
	a.tweetFetcher = f
	a.tweetURLExtract = extractURLs
}

func (a *Agent) SetMediaStore(s portmem.Media) {
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

// LastBackground returns the most recently injected background messages.
func (a *Agent) LastBackground() []message.Message {
	a.lastEphemeralMu.RLock()
	defer a.lastEphemeralMu.RUnlock()
	out := make([]message.Message, len(a.lastBackground))
	copy(out, a.lastBackground)
	return out
}

// LastForeground returns the most recently injected foreground messages.
func (a *Agent) LastForeground() []message.Message {
	a.lastEphemeralMu.RLock()
	defer a.lastEphemeralMu.RUnlock()
	out := make([]message.Message, len(a.lastForeground))
	copy(out, a.lastForeground)
	return out
}

// OnRoleSpecChanged は llm ロール変更時に必要な agent 側調整を行う。
// conversation ロールに限り、token counter の更新とコンテキストサイズ
// 調整、必要なら即時圧縮までを面倒見る。
// portllm.Client.SwapRoleSpec はこの呼び出しより前に済ませておくこと。
func (a *Agent) OnRoleSpecChanged(role string, spec portllm.RoleSpec) {
	if role != "conversation" {
		return
	}
	a.UpdateTokenCounter(spec.ProviderType, spec.ModelID)
	if spec.MaxContext <= 0 {
		return
	}
	a.AgentContext().SetMaxTokens(spec.MaxContext)
	if a.AgentContext().UsageRatio() > 0.5 {
		compactCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.ForceCompact(compactCtx)
	}
}

// UpdateTokenCounter はプロバイダタイプとモデル名からトークンカウンタを更新する。
// conversation ロールの swap 時に呼ぶ。
func (a *Agent) UpdateTokenCounter(providerType, model string) {
	counter := llmtoken.NewTokenCounter(providerType, model, a.logger)
	for _, ctx := range a.contexts {
		ctx.SetTokenCounter(counter)
	}
}
