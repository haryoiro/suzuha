package control

import (
	"context"
	"path/filepath"
	"time"

	"github.com/haryoiro/suzuha/internal/api/control/gen"
	convcap "github.com/haryoiro/suzuha/internal/capability/conversation"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/runtime/agent"
	"github.com/samber/do/v2"
)

// RuntimeHandler は Runtime グループ (compact / reload-*) を実装する。
type RuntimeHandler struct {
	agent        *agent.Agent
	channelStore *convcap.SettingsStore
	promptDir    string
	configDir    string
}

// NewRuntimeHandler は DI injector から依存を取り出して RuntimeHandler を生成する。
func NewRuntimeHandler(i do.Injector) (gen.RuntimeHandler, error) {
	cfg := do.MustInvoke[*config.Config](i)
	cfgPath := do.MustInvokeNamed[string](i, "config-path")
	return &RuntimeHandler{
		agent:        do.MustInvoke[*agent.Agent](i),
		channelStore: do.MustInvoke[*convcap.SettingsStore](i),
		promptDir:    cfg.Agent.PromptDir,
		configDir:    filepath.Dir(cfgPath),
	}, nil
}

// RuntimeReloadChannelSettings implements POST /internal/reload-channel-settings.
func (h *RuntimeHandler) RuntimeReloadChannelSettings(ctx context.Context) (*gen.OkResponse, error) {
	if err := h.channelStore.Reload(ctx); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// RuntimeCompact implements POST /internal/compact.
func (h *RuntimeHandler) RuntimeCompact(ctx context.Context) (*gen.CompactResponse, error) {
	compactCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	h.agent.ForceCompact(compactCtx)
	return &gen.CompactResponse{
		Ok:           true,
		MessageCount: int32(h.agent.AgentContext().Len()),
	}, nil
}

// RuntimeReloadPrompt implements POST /internal/reload-prompt.
func (h *RuntimeHandler) RuntimeReloadPrompt(ctx context.Context) (*gen.ReloadPromptResponse, error) {
	prompt, err := config.LoadPromptFiles(h.promptDir, h.configDir)
	if err != nil {
		return nil, err
	}
	h.agent.ReloadPrompt(prompt)
	return &gen.ReloadPromptResponse{
		Ok:     true,
		Length: int32(len(prompt)),
	}, nil
}
