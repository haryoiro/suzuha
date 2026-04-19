package control

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"database/sql"
	"log/slog"
	"sort"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/haryoiro/suzuha/internal/voice"
)

// Handler は control API の ogen Handler 実装。
// 各メソッドは gen が定義する interface を満たす。
type Handler struct {
	gen.UnimplementedHandler
	agent            *agent.Agent
	channelStore     *channel.Store
	userStore        user.Store
	scheduler        *scheduler.Scheduler
	voicevox         *tts.VoicevoxClient
	voicevoxCfg      *config.TTSProvider // 話者 ID 変更を config に反映するため
	voicePipeline    *voice.Pipeline     // runtime の話者切り替え用 (nil 可)
	toolRegistry     *tool.Registry
	sharedDB         *sql.DB // disabled tools 永続化用
	llmClient        *llm.Client
	providerRegistry *llm.ProviderRegistry
	logger           *slog.Logger
	promptDir        string
	configDir        string
}

// Config は Handler のコンストラクタ引数。
// 依存が多いので struct literal で渡す。nil 可なフィールドは nil 可と明記。
type Config struct {
	Agent            *agent.Agent
	ChannelStore     *channel.Store
	UserStore        user.Store
	Scheduler        *scheduler.Scheduler // nil: scheduler endpoint は空で返す
	Voicevox         *tts.VoicevoxClient  // nil: voicevox endpoint は 503
	VoicevoxCfg      *config.TTSProvider  // nil: voicevox endpoint は 503
	VoicePipeline    *voice.Pipeline      // nil: runtime 話者切替をスキップ
	ToolRegistry     *tool.Registry
	SharedDB         *sql.DB // tool.SaveDisabled 用
	LLMClient        *llm.Client
	ProviderRegistry *llm.ProviderRegistry
	Logger           *slog.Logger
	PromptDir        string
	ConfigDir        string
}

// NewHandler は Control API のハンドラを生成する。
func NewHandler(cfg Config) *Handler {
	return &Handler{
		agent:            cfg.Agent,
		channelStore:     cfg.ChannelStore,
		userStore:        cfg.UserStore,
		scheduler:        cfg.Scheduler,
		voicevox:         cfg.Voicevox,
		voicevoxCfg:      cfg.VoicevoxCfg,
		voicePipeline:    cfg.VoicePipeline,
		toolRegistry:     cfg.ToolRegistry,
		sharedDB:         cfg.SharedDB,
		llmClient:        cfg.LLMClient,
		providerRegistry: cfg.ProviderRegistry,
		logger:           cfg.Logger,
		promptDir:        cfg.PromptDir,
		configDir:        cfg.ConfigDir,
	}
}

// RuntimeReloadChannelSettings implements POST /internal/reload-channel-settings.
func (h *Handler) RuntimeReloadChannelSettings(ctx context.Context) (*gen.OkResponse, error) {
	if err := h.channelStore.Reload(ctx); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// RuntimeCompact implements POST /internal/compact.
func (h *Handler) RuntimeCompact(ctx context.Context) (*gen.CompactResponse, error) {
	// 圧縮は時間がかかるので ctx とは別に 5 分タイムアウトを設ける。
	compactCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	h.agent.ForceCompact(compactCtx)
	return &gen.CompactResponse{
		Ok:           true,
		MessageCount: int32(h.agent.AgentContext().Len()),
	}, nil
}

// RuntimeReloadPrompt implements POST /internal/reload-prompt.
func (h *Handler) RuntimeReloadPrompt(ctx context.Context) (*gen.ReloadPromptResponse, error) {
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

// SchedulerJobs implements GET /internal/scheduler/jobs.
func (h *Handler) SchedulerJobs(ctx context.Context) (*gen.SchedulerJobsResponse, error) {
	if h.scheduler == nil {
		return &gen.SchedulerJobsResponse{Data: []gen.SchedulerJob{}}, nil
	}
	jobs := h.scheduler.ListJobs()
	data := make([]gen.SchedulerJob, len(jobs))
	for i, j := range jobs {
		data[i] = gen.SchedulerJob{
			Name: j.Name,
			Task: j.Task,
			Cron: j.Cron,
			Prev: j.Prev.Format(time.RFC3339),
			Next: j.Next.Format(time.RFC3339),
		}
		if j.Config != nil {
			data[i].Config = gen.NewOptSchedulerJobConfig(toJxMap(j.Config))
		}
	}
	return &gen.SchedulerJobsResponse{Data: data}, nil
}

// SchedulerTrigger implements POST /internal/trigger/{task}.
func (h *Handler) SchedulerTrigger(ctx context.Context, req *gen.TriggerRequest, params gen.SchedulerTriggerParams) (*gen.TriggerResponse, error) {
	if h.scheduler == nil {
		msg := "scheduler not enabled"
		return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString(msg)}, nil
	}
	var cfg json.RawMessage
	if req != nil && req.Config.Set {
		// Ogen generates OptTriggerRequestConfig wrapping map[string]jx.Raw. Serialize to JSON.
		b, err := json.Marshal(req.Config.Value)
		if err != nil {
			return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString("config marshal failed")}, nil
		}
		cfg = b
	}
	if err := h.scheduler.TriggerTask(ctx, params.Task, cfg); err != nil {
		return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString(err.Error())}, nil
	}
	return &gen.TriggerResponse{Ok: true}, nil
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

// VoicevoxSpeakers implements GET /internal/voicevox/speakers.
func (h *Handler) VoicevoxSpeakers(ctx context.Context) ([]gen.VoicevoxSpeakersOKItem, error) {
	if h.voicevox == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	raw, err := h.voicevox.ListSpeakers(ctx)
	if err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]gen.VoicevoxSpeakersOKItem, len(items))
	for i, item := range items {
		m := make(gen.VoicevoxSpeakersOKItem, len(item))
		for k, v := range item {
			m[k] = jx.Raw(v)
		}
		out[i] = m
	}
	return out, nil
}

// VoicevoxGetSpeaker implements GET /internal/voicevox/speaker.
func (h *Handler) VoicevoxGetSpeaker(ctx context.Context) (*gen.VoicevoxSpeaker, error) {
	if h.voicevoxCfg == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	return &gen.VoicevoxSpeaker{SpeakerID: int32(h.voicevoxCfg.SpeakerID)}, nil
}

// VoicevoxSetSpeaker implements PUT /internal/voicevox/speaker.
func (h *Handler) VoicevoxSetSpeaker(ctx context.Context, req *gen.SetSpeakerRequest) (*gen.OkResponse, error) {
	if h.voicevoxCfg == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	id := int(req.SpeakerID)
	h.voicevoxCfg.SpeakerID = id
	if h.voicePipeline != nil {
		h.voicePipeline.SetSpeakerID(id)
	}
	return &gen.OkResponse{Ok: true}, nil
}

// ToolsList implements GET /internal/tools.
func (h *Handler) ToolsList(ctx context.Context) (*gen.ToolsListResponse, error) {
	tools := h.toolRegistry.All()
	out := make([]gen.ToolInfo, 0, len(tools))
	for _, t := range tools {
		schema := gen.ToolInfoInputSchema{}
		if raw := t.InputSchema(); len(raw) > 0 {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err == nil {
				for k, v := range m {
					schema[k] = jx.Raw(v)
				}
			}
		}
		out = append(out, gen.ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: schema,
			Enabled:     !h.toolRegistry.IsDisabled(t.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return &gen.ToolsListResponse{Data: out}, nil
}

// ToolsSetEnabled implements PUT /internal/tools/{name}/enabled.
func (h *Handler) ToolsSetEnabled(ctx context.Context, req *gen.SetToolEnabledRequest, params gen.ToolsSetEnabledParams) (*gen.OkResponse, error) {
	h.toolRegistry.SetEnabled(params.Name, req.Enabled)
	if err := tool.SaveDisabled(ctx, h.sharedDB, h.toolRegistry.DisabledNames()); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// ToolsExecute implements POST /internal/tools/{name}/execute.
func (h *Handler) ToolsExecute(ctx context.Context, req gen.ToolsExecuteReq, params gen.ToolsExecuteParams) (*gen.ToolExecuteResponse, error) {
	t, ok := h.toolRegistry.Get(params.Name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", params.Name)
	}
	input := []byte("{}")
	if len(req) > 0 {
		m := make(map[string]json.RawMessage, len(req))
		for k, v := range req {
			m[k] = json.RawMessage(v)
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		input = b
	}
	result, err := t.Execute(ctx, json.RawMessage(input))
	if err != nil {
		return nil, err
	}
	var text string
	for _, c := range result.Content {
		text += c.Text
	}
	return &gen.ToolExecuteResponse{
		Ok:      !result.IsError,
		Output:  text,
		IsError: result.IsError,
	}, nil
}

// LLMStatus implements GET /internal/llm.
func (h *Handler) LLMStatus(ctx context.Context) (*gen.LLMStatus, error) {
	prov, model, apiBase, vision := h.llmClient.ProviderInfo()
	assignments, err := h.providerRegistry.Assignments(ctx)
	if err != nil {
		return nil, err
	}
	return &gen.LLMStatus{
		Provider:    prov,
		ModelID:     model,
		APIBase:     apiBase,
		MaxCtx:      int32(h.llmClient.MaxContextTokens()),
		Vision:      vision,
		Assignments: structSliceToJxItems[gen.LLMStatusAssignmentsItem](assignments),
	}, nil
}

// LLMListProviders implements GET /internal/llm/providers.
func (h *Handler) LLMListProviders(ctx context.Context) ([]gen.LLMListProvidersOKItem, error) {
	providers, err := h.providerRegistry.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListProvidersOKItem](providers), nil
}

// LLMListModels implements GET /internal/llm/models.
func (h *Handler) LLMListModels(ctx context.Context, params gen.LLMListModelsParams) ([]gen.LLMListModelsOKItem, error) {
	providerFilter := params.Provider.Or("")
	models, err := h.providerRegistry.ListModels(ctx, providerFilter)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListModelsOKItem](models), nil
}

// LLMSaveModel implements POST /internal/llm/models.
func (h *Handler) LLMSaveModel(ctx context.Context, req *gen.SaveModelRequest) (*gen.OkResponse, error) {
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
	if err := h.providerRegistry.SaveModel(ctx, m); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// LLMRefreshModels implements POST /internal/llm/models/refresh.
// 全プロバイダの API からモデル一覧を再取得して upsert する。
func (h *Handler) LLMRefreshModels(ctx context.Context) (*gen.ModelsRefreshResponse, error) {
	providers, err := h.providerRegistry.ListProviders(ctx)
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
			if h.logger != nil {
				h.logger.Warn("モデルカタログ更新失敗", "provider", p.Name, "error", err)
			}
			continue
		}
		for i := range models {
			models[i].ProviderName = p.Name
			if err := h.providerRegistry.SaveModel(ctx, &models[i]); err != nil {
				if h.logger != nil {
					h.logger.Warn("モデル保存失敗", "provider", p.Name, "model", models[i].ModelID, "error", err)
				}
				continue
			}
			total++
		}
	}
	return &gen.ModelsRefreshResponse{Ok: true, ModelsUpdated: int32(total)}, nil
}

// LLMListRoles implements GET /internal/llm/roles.
func (h *Handler) LLMListRoles(ctx context.Context) ([]gen.LLMListRolesOKItem, error) {
	assignments, err := h.providerRegistry.Assignments(ctx)
	if err != nil {
		return nil, err
	}
	return structSliceToJxItems[gen.LLMListRolesOKItem](assignments), nil
}

// LLMAssignRole implements PUT /internal/llm/roles/{role}.
// ロール割当を DB に保存し、runtime 側にも反映する。conversation ロール
// 変更時は agent 側の token counter / max tokens / 必要なら圧縮も走る。
func (h *Handler) LLMAssignRole(ctx context.Context, req *gen.AssignRoleRequest, params gen.LLMAssignRoleParams) (*gen.OkResponse, error) {
	if req.Provider == "" || req.ModelID == "" {
		return nil, fmt.Errorf("provider and model_id required")
	}
	spec, err := h.providerRegistry.BuildRoleSpec(ctx, req.Provider, req.ModelID)
	if err != nil {
		return nil, err
	}
	if err := h.providerRegistry.AssignRole(ctx, params.Role, req.Provider, req.ModelID); err != nil {
		return nil, err
	}
	h.llmClient.SwapRoleSpec(params.Role, *spec)
	h.agent.OnRoleSpecChanged(params.Role, *spec)
	return &gen.OkResponse{Ok: true}, nil
}

// structSliceToJxItems は struct のスライスを JSON 経由で ogen の
// map[string]jx.Raw スライスに変換する。ogen の Record<unknown>[] 返値用。
// ItemT は具体的な gen.XxxItem 型 (map[string]jx.Raw を alias したもの)。
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

// toJxMap は map[string]any を ogen の map[string]jx.Raw に変換する。
// Marshal 失敗時はその key をスキップ。
func toJxMap(m map[string]any) gen.SchedulerJobConfig {
	out := make(gen.SchedulerJobConfig, len(m))
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out[k] = jx.Raw(b)
	}
	return out
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
