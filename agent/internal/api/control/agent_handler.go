package control

import (
	"context"

	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/gateway"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/port/user"
	"github.com/samber/do/v2"
)

// AgentHandler は Agent グループ (identity / context / gateway status) を実装する。
type AgentHandler struct {
	agent     *agent.Agent
	userStore user.Store
	gateway   *gateway.Gateway
}

// NewAgentHandler は DI injector から依存を取り出して AgentHandler を生成する。
func NewAgentHandler(i do.Injector) (gen.AgentHandler, error) {
	return &AgentHandler{
		agent:     do.MustInvoke[*agent.Agent](i),
		userStore: do.MustInvoke[user.Store](i),
		gateway:   do.MustInvoke[*gateway.Gateway](i),
	}, nil
}

// AgentOpsIdentity implements GET /internal/identity.
func (h *AgentHandler) AgentOpsIdentity(ctx context.Context) (*gen.Identity, error) {
	botPlatformID := h.agent.BotID()
	resp := &gen.Identity{BotPlatformID: botPlatformID}
	if botPlatformID == "" {
		return resp, nil
	}
	// GET は副作用を避けるため ResolveExisting (作成しない) を使う。
	u, err := h.userStore.ResolveExisting(ctx, "discord", botPlatformID)
	if err != nil {
		return resp, nil
	}
	resp.BotUserID = gen.NewOptString(u.ID)
	resp.BotName = gen.NewOptString(u.DisplayName)
	return resp, nil
}

// AgentOpsGetContext implements GET /internal/context.
func (h *AgentHandler) AgentOpsGetContext(ctx context.Context) (*gen.AgentContext, error) {
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

// AgentOpsGatewayStatus implements GET /internal/gateway/status.
func (h *AgentHandler) AgentOpsGatewayStatus(ctx context.Context) ([]gen.GatewayStatusItem, error) {
	statuses := h.gateway.Status()
	out := make([]gen.GatewayStatusItem, len(statuses))
	for i, s := range statuses {
		item := gen.GatewayStatusItem{
			Name:  s.Name,
			State: string(s.State),
		}
		if s.StartedAt != nil {
			item.StartedAt = gen.NewOptString(s.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
		}
		if s.Error != "" {
			item.Error = gen.NewOptString(s.Error)
		}
		out[i] = item
	}
	return out, nil
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
