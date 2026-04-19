package control

import (
	"context"

	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/user"
)

// Handler は control API の ogen Handler 実装。
// 各メソッドは gen が定義する interface を満たす。
type Handler struct {
	gen.UnimplementedHandler
	agent        *agent.Agent
	channelStore *channel.Store
	userStore    user.Store
}

// NewHandler は Control API のハンドラを生成する。
func NewHandler(ag *agent.Agent, channelStore *channel.Store, userStore user.Store) *Handler {
	return &Handler{agent: ag, channelStore: channelStore, userStore: userStore}
}

// RuntimeReloadChannelSettings implements POST /internal/reload-channel-settings.
func (h *Handler) RuntimeReloadChannelSettings(ctx context.Context) (*gen.OkResponse, error) {
	if err := h.channelStore.Reload(ctx); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// AgentOpsIdentity implements GET /internal/identity.
func (h *Handler) AgentOpsIdentity(ctx context.Context) (*gen.Identity, error) {
	botPlatformID := h.agent.BotID()
	resp := &gen.Identity{BotPlatformID: botPlatformID}
	if botPlatformID == "" {
		return resp, nil
	}
	// GET は副作用を避けるため ResolveExisting (作成しない) を使う。
	u, err := h.userStore.ResolveExisting(ctx, "discord", botPlatformID)
	if err != nil {
		// 見つからないのは正常ケースなので無視して platform_id のみ返す。
		return resp, nil
	}
	resp.BotUserID = gen.NewOptString(u.ID)
	resp.BotName = gen.NewOptString(u.DisplayName)
	return resp, nil
}

// AgentOpsGetContext implements GET /internal/context.
func (h *Handler) AgentOpsGetContext(ctx context.Context) (*gen.AgentContext, error) {
	actx := h.agent.AgentContext()
	msgs := actx.MessagesWithSystem()
	return &gen.AgentContext{
		Messages:        toContextMessages(msgs),
		Count:           int32(len(msgs)),
		EstimatedTokens: int32(actx.ActualTokens()),
		UsageRatio:      actx.UsageRatio(),
		MaxTokens:       int32(actx.MaxTokens()),
		Background:      toContextMessages(h.agent.LastBackground()),
		Foreground:      toContextMessages(h.agent.LastForeground()),
	}, nil
}

func toContextMessages(msgs []llm.Message) []gen.ContextMessage {
	out := make([]gen.ContextMessage, len(msgs))
	for i, m := range msgs {
		out[i] = gen.ContextMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.Channel != "" {
			out[i].Channel = gen.NewOptString(m.Channel)
		}
		if m.ChannelName != "" {
			out[i].ChannelName = gen.NewOptString(m.ChannelName)
		}
		if !m.Timestamp.IsZero() {
			out[i].Timestamp = gen.NewOptString(m.Timestamp.Format("2006-01-02T15:04:05Z07:00"))
		}
	}
	return out
}
