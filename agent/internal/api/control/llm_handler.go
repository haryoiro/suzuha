package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/runtime/agent"
	"github.com/samber/do/v2"
)

// LLMHandler は LLM グループ (status / providers / models / roles) を実装する。
type LLMHandler struct {
	agent    *agent.Agent
	client   *llm.Client
	registry *llm.ProviderRegistry
	logger   *slog.Logger
}

// NewLLMHandler は DI injector から依存を取り出して LLMHandler を生成する。
func NewLLMHandler(i do.Injector) (gen.LLMHandler, error) {
	return &LLMHandler{
		agent:    do.MustInvoke[*agent.Agent](i),
		client:   do.MustInvoke[*llm.Client](i),
		registry: do.MustInvoke[*llm.ProviderRegistry](i),
		logger:   do.MustInvoke[*slog.Logger](i),
	}, nil
}

// LLMStatus implements GET /internal/llm.
func (h *LLMHandler) LLMStatus(ctx context.Context) (*gen.LLMStatus, error) {
	prov, model, apiBase, vision := h.client.ProviderInfo()
	assignments, err := h.registry.Assignments(ctx)
	if err != nil {
		return nil, err
	}
	return &gen.LLMStatus{
		Provider:    prov,
		ModelID:     model,
		APIBase:     apiBase,
		MaxCtx:      int32(h.client.MaxContextTokens()),
		Vision:      vision,
		Assignments: structSliceToJxItems[gen.LLMStatusAssignmentsItem](assignments),
	}, nil
}

// LLMListProviders implements GET /internal/llm/providers.
func (h *LLMHandler) LLMListProviders(ctx context.Context) ([]gen.LLMListProvidersOKItem, error) {
	providers, err := h.registry.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListProvidersOKItem](providers), nil
}

// LLMListModels implements GET /internal/llm/models.
func (h *LLMHandler) LLMListModels(ctx context.Context, params gen.LLMListModelsParams) ([]gen.LLMListModelsOKItem, error) {
	providerFilter := params.Provider.Or("")
	models, err := h.registry.ListModels(ctx, providerFilter)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListModelsOKItem](models), nil
}

// LLMSaveModel implements POST /internal/llm/models.
func (h *LLMHandler) LLMSaveModel(ctx context.Context, req *gen.SaveModelRequest) (*gen.OkResponse, error) {
	if req.ProviderName == "" || req.ModelID == "" {
		return nil, fmt.Errorf("provider_name and model_id required")
	}
	m := &llm.ModelInfo{
		ProviderName: req.ProviderName,
		ModelID:      req.ModelID,
		Capabilities: req.Capabilities,
	}
	if len(m.Capabilities) == 0 {
		m.Capabilities = []string{"text"}
	}
	if v, ok := req.MaxContext.Get(); ok {
		m.MaxContext = int(v)
	}
	if v, ok := req.Source.Get(); ok {
		m.Source = v
	}
	if err := h.registry.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// LLMRefreshModels implements POST /internal/llm/models/refresh.
func (h *LLMHandler) LLMRefreshModels(ctx context.Context) (*gen.ModelsRefreshResponse, error) {
	providers, err := h.registry.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	var total int
	for _, p := range providers {
		meta := llm.GetProviderMeta(p.Type)
		if meta == nil {
			continue
		}
		models, err := meta.ListModels(ctx, p.APIKey, p.APIBase)
		if err != nil {
			h.logger.Warn("モデルカタログ更新失敗", "provider", p.Name, "error", err)
			continue
		}
		for i := range models {
			models[i].ProviderName = p.Name
			if err := h.registry.SaveModel(ctx, &models[i]); err != nil {
				h.logger.Warn("モデル保存失敗", "provider", p.Name, "model", models[i].ModelID, "error", err)
				continue
			}
			total++
		}
	}
	return &gen.ModelsRefreshResponse{Ok: true, ModelsUpdated: int32(total)}, nil
}

// LLMListRoles implements GET /internal/llm/roles.
func (h *LLMHandler) LLMListRoles(ctx context.Context) ([]gen.LLMListRolesOKItem, error) {
	assignments, err := h.registry.Assignments(ctx)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListRolesOKItem](assignments), nil
}

// LLMAssignRole implements PUT /internal/llm/roles/{role}.
func (h *LLMHandler) LLMAssignRole(ctx context.Context, req *gen.AssignRoleRequest, params gen.LLMAssignRoleParams) (*gen.OkResponse, error) {
	if req.Provider == "" || req.ModelID == "" {
		return nil, fmt.Errorf("provider and model_id required")
	}
	spec, err := h.registry.BuildRoleSpec(ctx, req.Provider, req.ModelID)
	if err != nil {
		return nil, err
	}
	if err := h.registry.AssignRole(ctx, params.Role, req.Provider, req.ModelID); err != nil {
		return nil, err
	}
	h.client.SwapRoleSpec(params.Role, *spec)
	h.agent.OnRoleSpecChanged(params.Role, *spec)
	return &gen.OkResponse{Ok: true}, nil
}

// structToJxMap は struct を JSON 経由で map[string]jx.Raw に変換する。
// Record<unknown> 型の単一レスポンスフィールド用。
func structToJxMap[MapT ~map[string]jx.Raw, T any](v T) MapT {
	raw, err := json.Marshal(v)
	if err != nil {
		return MapT{}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return MapT{}
	}
	out := make(MapT, len(m))
	for k, v := range m {
		out[k] = jx.Raw(v)
	}
	return out
}

// structSliceToJxItems は struct のスライスを JSON 経由で ogen の
// map[string]jx.Raw スライスに変換する。Record<unknown>[] 返値の合成用。
// ItemT は gen の map[string]jx.Raw 型エイリアス (LLMListProvidersOKItem 等)。
func structSliceToJxItems[ItemT ~map[string]jx.Raw, T any](items []T) []ItemT {
	if len(items) == 0 {
		return []ItemT{}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return []ItemT{}
	}
	var maps []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &maps); err != nil {
		return []ItemT{}
	}
	out := make([]ItemT, len(maps))
	for i, m := range maps {
		item := make(ItemT, len(m))
		for k, v := range m {
			item[k] = jx.Raw(v)
		}
		out[i] = item
	}
	return out
}
