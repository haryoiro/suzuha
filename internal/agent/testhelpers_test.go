package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/event"
	acq "github.com/haryoiro/suzuha/internal/memento/acquirer"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
)

// --- Mock memory.Store ---

type mockMemory struct{}

func (m *mockMemory) Save(_ context.Context, _ *memory.Memory) error { return nil }
func (m *mockMemory) Search(_ context.Context, _ string, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchWithContext(_ context.Context, _ string, _ int, _ memory.SymbolicFilter) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchByType(_ context.Context, _ string, _ memory.MemoryType, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchRecent(_ context.Context, _ string, _ int, _ time.Time) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListByUser(_ context.Context, _ string, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListEpisodesByParticipant(_ context.Context, _ string, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListByType(_ context.Context, _ memory.MemoryType, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListRecentByType(_ context.Context, _ memory.MemoryType, _ time.Time, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) ListRecent(_ context.Context, _ time.Time, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) SearchByParts(_ context.Context, _ []embedding.Part, _ int) ([]memory.Memory, error) {
	return nil, nil
}
func (m *mockMemory) IsDuplicate(_ context.Context, _ string, _ memory.MemoryType) (string, []float32, error) {
	return "", nil, nil
}
func (m *mockMemory) IsDuplicateBatch(_ context.Context, candidates []memory.DupCandidate) ([]memory.DupResult, error) {
	return make([]memory.DupResult, len(candidates)), nil
}
func (m *mockMemory) Close() error { return nil }

var _ memory.Store = (*mockMemory)(nil)

// --- Mock user.Store ---

type mockUsers struct {
	resolveUser *user.User // returned by Resolve
}

func (m *mockUsers) Resolve(_ context.Context, _, _, _ string) (*user.User, error) {
	if m.resolveUser != nil {
		return m.resolveUser, nil
	}
	return &user.User{ID: "u1", DisplayName: "TestUser"}, nil
}
func (m *mockUsers) Get(_ context.Context, _ string) (*user.User, error) {
	return &user.User{ID: "u1"}, nil
}
func (m *mockUsers) UpdateDisplayName(_ context.Context, _, _ string) error          { return nil }
func (m *mockUsers) TrackGuildChannel(_ context.Context, _, _, _, _, _ string) error { return nil }
func (m *mockUsers) GetUserGuilds(_ context.Context, _ string) ([]user.UserGuild, error) {
	return nil, nil
}
func (m *mockUsers) ResolveExisting(_ context.Context, _, _ string) (*user.User, error) {
	return &user.User{ID: "u1"}, nil
}
func (m *mockUsers) ListMentionable(_ context.Context) ([]user.MentionableUser, error) {
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
	acquireResult *acq.AcquireResult
}

func (m *mockAcquirer) Acquire(_ context.Context, _ *acq.AcquireRequest) (*acq.AcquireResult, error) {
	if m.acquireResult != nil {
		return m.acquireResult, nil
	}
	return &acq.AcquireResult{}, nil
}

// --- Test Agent builder ---

func newTestAgent(opts ...func(*Agent)) *Agent {
	bus := event.NewBus(16)
	mc := &mockChat{}
	regs := []SourceRegistration{
		{
			Key: SourceKeyDiscord,
			NewSession: func(agentCtx *Context) Session {
				return NewDiscordSession(agentCtx, mc, nil, nil, DefaultDrainWindow, slog.Default())
			},
			PersistKey: "discord",
		},
		{
			Key: SourceKeyDevice,
			NewSession: func(agentCtx *Context) Session {
				return NewDeviceSession(agentCtx, nil, slog.Default())
			},
			PersistKey: "device",
		},
		{
			Key: SourceKeyWeb,
			NewSession: func(agentCtx *Context) Session {
				return NewWebSession(agentCtx, nil, slog.Default())
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
		nil, // llm.Client — nil is OK when we don't call Act
		tool.NewRegistry(),
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
