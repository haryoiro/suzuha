package control

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"database/sql"
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
	agent         *agent.Agent
	channelStore  *channel.Store
	userStore     user.Store
	scheduler     *scheduler.Scheduler
	voicevox      *tts.VoicevoxClient
	voicevoxCfg   *config.TTSProvider // 話者 ID 変更を config に反映するため
	voicePipeline *voice.Pipeline     // runtime の話者切り替え用 (nil 可)
	toolRegistry  *tool.Registry
	sharedDB      *sql.DB // disabled tools 永続化用
	promptDir     string
	configDir     string
}

// Config は Handler のコンストラクタ引数。
// 依存が多いので struct literal で渡す。nil 可なフィールドは nil 可と明記。
type Config struct {
	Agent         *agent.Agent
	ChannelStore  *channel.Store
	UserStore     user.Store
	Scheduler     *scheduler.Scheduler  // nil: scheduler endpoint は空で返す
	Voicevox      *tts.VoicevoxClient   // nil: voicevox endpoint は 503
	VoicevoxCfg   *config.TTSProvider   // nil: voicevox endpoint は 503
	VoicePipeline *voice.Pipeline       // nil: runtime 話者切替をスキップ
	ToolRegistry  *tool.Registry
	SharedDB      *sql.DB // tool.SaveDisabled 用
	PromptDir     string
	ConfigDir     string
}

// NewHandler は Control API のハンドラを生成する。
func NewHandler(cfg Config) *Handler {
	return &Handler{
		agent:         cfg.Agent,
		channelStore:  cfg.ChannelStore,
		userStore:     cfg.UserStore,
		scheduler:     cfg.Scheduler,
		voicevox:      cfg.Voicevox,
		voicevoxCfg:   cfg.VoicevoxCfg,
		voicePipeline: cfg.VoicePipeline,
		toolRegistry:  cfg.ToolRegistry,
		sharedDB:      cfg.SharedDB,
		promptDir:     cfg.PromptDir,
		configDir:     cfg.ConfigDir,
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
