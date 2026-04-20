package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	userdom "github.com/haryoiro/suzuha/internal/domain/user"
	embedding "github.com/haryoiro/suzuha/internal/port/embedder"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	"github.com/haryoiro/suzuha/internal/port/user"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	toolreg "github.com/haryoiro/suzuha/internal/runtime/toolregistry"
)

// --- Mock memory.Store ---

type mockMemory struct{}

func (m *mockMemory) Save(_ context.Context, _ *memo.Memory) error { return nil }
func (m *mockMemory) Search(_ context.Context, _ string, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchWithContext(_ context.Context, _ string, _ int, _ memo.SymbolicFilter) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchByType(_ context.Context, _ string, _ memo.MemoryType, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchRecent(_ context.Context, _ string, _ int, _ time.Time) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchByParts(_ context.Context, _ []embedding.Part, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListByUser(_ context.Context, _ string, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListEpisodesByParticipant(_ context.Context, _ string, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListByType(_ context.Context, _ memo.MemoryType, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListRecentByType(_ context.Context, _ memo.MemoryType, _ time.Time, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListRecent(_ context.Context, _ time.Time, _ int) ([]memo.Memory, error) {
	return nil, nil
}
func (m *mockMemory) IsDuplicate(_ context.Context, _ string, _ memo.MemoryType) (string, []float32, error) {
	return "", nil, nil
}
func (m *mockMemory) IsDuplicateBatch(_ context.Context, candidates []memo.DupCandidate) ([]memo.DupResult, error) {
	return make([]memo.DupResult, len(candidates)), nil
}
func (m *mockMemory) Close() error { return nil }

var _ portmem.Memory = (*mockMemory)(nil)

// --- Mock user.Store ---

type mockUsers struct {
	resolveUser *userdom.User // returned by Resolve
}

func (m *mockUsers) Resolve(_ context.Context, _, _, _ string) (*userdom.User, error) {
	if m.resolveUser != nil {
		return m.resolveUser, nil
	}
	return &userdom.User{ID: "u1", DisplayName: "TestUser"}, nil
}
func (m *mockUsers) Get(_ context.Context, _ string) (*userdom.User, error) {
	return &userdom.User{ID: "u1"}, nil
}
func (m *mockUsers) UpdateDisplayName(_ context.Context, _, _ string) error          { return nil }
func (m *mockUsers) TrackGuildChannel(_ context.Context, _, _, _, _, _ string) error { return nil }
func (m *mockUsers) GetUserGuilds(_ context.Context, _ string) ([]userdom.UserGuild, error) {
	return nil, nil
}
func (m *mockUsers) ResolveExisting(_ context.Context, _, _ string) (*userdom.User, error) {
	return &userdom.User{ID: "u1"}, nil
}
func (m *mockUsers) ListMentionable(_ context.Context) ([]userdom.MentionableUser, error) {
	return nil, nil
}
func (m *mockUsers) Close() error { return nil }

var _ user.Store = (*mockUsers)(nil)

// --- Mock chat.Interface ---

type mockChat struct {
	sent []string
}

func (m *mockChat) Run(_ context.Context) error { return nil }
func (m *mockChat) Send(_ context.Context, _, text string) error {
	m.sent = append(m.sent, text)
	return nil
}

// --- Mock acquirer ---

type mockAcquirer struct {
	acquireResult *portmem.AcquireResult
}

func (m *mockAcquirer) Acquire(_ context.Context, _ *portmem.AcquireRequest) (*portmem.AcquireResult, error) {
	if m.acquireResult != nil {
		return m.acquireResult, nil
	}
	return &portmem.AcquireResult{}, nil
}

// --- testSession: Session interface の最小テスト実装 ---

type testSession struct {
	agentCtx    *Context
	src         SourceKey
	persistKey  string
	drainWindow time.Duration
}

func (s *testSession) Source() SourceKey     { return s.src }
func (s *testSession) Context() *Context     { return s.agentCtx }
func (s *testSession) PersistKey() string    { return s.persistKey }
func (s *testSession) BeginTurn(*Perception) {}
func (s *testSession) DirectiveConfig() DirectiveConfig {
	switch s.src {
	case SourceKeyDevice:
		return DeviceDirectiveConfig()
	case SourceKeyWeb:
		return WebDirectiveConfig()
	default:
		return DiscordDirectiveConfig(s.drainWindow)
	}
}
func (s *testSession) Respond(_ context.Context, _ string) error { return nil }

// --- Test Agent builder ---

func newTestAgent(opts ...func(*Agent)) *Agent {
	bus := event.NewBus(16)
	mc := &mockChat{}
	// testSession は channel/* 実装に依存せず Session interface を満たす最小実装。
	// import cycle 回避のため package agent 内で簡易実装する。
	_ = mc // mock chat は respond 経路で参照させない — 必要なら respondFn に差し替える
	regs := []SourceRegistration{
		{
			Key: SourceKeyDiscord,
			NewSession: func(agentCtx *Context) Session {
				return &testSession{agentCtx: agentCtx, src: SourceKeyDiscord, persistKey: "discord", drainWindow: DefaultDrainWindow}
			},
			PersistKey: "discord",
		},
		{
			Key: SourceKeyDevice,
			NewSession: func(agentCtx *Context) Session {
				return &testSession{agentCtx: agentCtx, src: SourceKeyDevice, persistKey: "device"}
			},
			PersistKey: "device",
		},
		{
			Key: SourceKeyWeb,
			NewSession: func(agentCtx *Context) Session {
				return &testSession{agentCtx: agentCtx, src: SourceKeyWeb, persistKey: "web"}
			},
			PersistKey: "web",
		},
	}
	ag := New(
		Config{
			SystemPrompt:     "You are a test bot.",
			BotID:            "bot123",
			ContextWindowPct: 0.8,
			MaxContextTokens: 10000,
		},
		regs,
		nil, // portllm.Client — nil is OK when we don't call Act
		toolreg.NewRegistry(),
		&mockMemory{},
		&mockUsers{},
		bus,
		&mockAcquirer{},
		nil, // convStore — nil is OK for tests
		nil, // db — nil is OK for tests that don't track channel activity
		nil, // channelSettings — nil skips channel filtering
		slog.Default(),
	)
	for _, o := range opts {
		o(ag)
	}
	return ag
}

// makeMessageEvent creates a test event with the given content and channel.
func makeMessageEvent(content, channel, userID string) event.Event {
	return event.NewMessageEvent("discord", event.MessagePayload{
		Content:  content,
		Channel:  channel,
		UserID:   userID,
		UserName: "tester",
	})
}

// makeDirectMessageEvent creates a DM event.
func makeDirectMessageEvent(content, userID string) event.Event {
	return event.NewMessageEvent("discord", event.MessagePayload{
		Content:  content,
		Channel:  "dm-channel",
		UserID:   userID,
		UserName: "tester",
		IsDM:     true,
	})
}

// makeMentionEvent creates a mention event.
func makeMentionEvent(content, channel, userID string) event.Event {
	return event.NewMessageEvent("discord", event.MessagePayload{
		Content:   content,
		Channel:   channel,
		UserID:    userID,
		UserName:  "tester",
		IsMention: true,
	})
}
