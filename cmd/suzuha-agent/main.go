package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/device"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/location"
	"github.com/haryoiro/suzuha/internal/mcp"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/selfimprove"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/haryoiro/suzuha/internal/voice"
	"github.com/samber/do/v2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := "config.yaml"
	if p := os.Getenv("SUZUHA_CONFIG"); p != "" {
		cfgPath = p
	}

	// Build DI container with all providers.
	injector := do.New(allPackages(cfgPath)...)

	// Eager resolve: fail fast on misconfiguration.
	cfg := do.MustInvoke[*config.Config](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	store := do.MustInvoke[*memory.SQLiteStore](injector)
	defer store.Close()

	_ = do.MustInvoke[*observe.Metrics](injector)

	mcpMgr := do.MustInvoke[*mcp.Manager](injector)
	defer mcpMgr.Close()

	chatIface := do.MustInvoke[chat.Interface](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	_ = do.MustInvoke[[]scheduler.Feature](injector) // triggers feature setup + tool/hook registration

	sched := do.MustInvoke[*scheduler.Scheduler](injector)
	if sched != nil {
		sched.Start()
		logger.Info("scheduler started", "jobs", len(cfg.Consolidator.Scheduler.Jobs))
		defer func() {
			sched.Stop()
			logger.Info("scheduler stopped")
		}()
	}

	// Register Discord OnReady callback.
	dc := do.MustInvoke[*discord.Chat](injector)
	if dc != nil {
		registerDiscordOnReady(injector, dc)
	}

	// Start internal HTTP server.
	if cfg.Observe.MetricsAddr != "" {
		go startInternalHTTP(injector, cfgPath)
	}

	// Start admin HTTP server.
	adminSrv := do.MustInvoke[*admin.Server](injector)
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil {
			logger.Error("admin server failed", "error", err)
		}
	}()

	// Context with signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start background embedding worker.
	go store.RunEmbeddingWorker(ctx)

	// Periodic reload of channel settings.
	channelStore := do.MustInvoke[*channel.Store](injector)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := channelStore.Reload(context.Background()); err != nil {
					logger.Warn("channel settings periodic reload failed", "error", err)
				}
			}
		}
	}()

	// Start chat interface in background.
	go func() {
		if err := chatIface.Run(ctx); err != nil {
			logger.Error("chat interface stopped", "error", err)
			cancel()
		}
	}()

	logger.Info("suzuha-agent started")
	return ag.Run(ctx)
}

func registerDiscordOnReady(injector do.Injector, dc *discord.Chat) {
	registry := do.MustInvoke[*tool.Registry](injector)
	userStore := do.MustInvoke[*user.SQLiteStore](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	cfg := do.MustInvoke[*config.Config](injector)

	dc.OnReady(func(s *discordgo.Session) {
		// Register Discord-specific tools.
		for _, t := range builtin.NewDiscordTools(s) {
			registry.Register(t)
		}
		logger.Info("discord tools registered")

		// Self-improvement tools.
		if cfg.SelfImprove.ChannelID != "" {
			registry.Register(selfimprove.NewImproveTool(s, cfg.SelfImprove.ChannelID))
			registry.Register(selfimprove.NewStatusTool("/app"))
			logger.Info("self-improve tools registered", "channel_id", cfg.SelfImprove.ChannelID)
		}

		// Voice chat setup.
		if cfg.Voice.Enabled {
			sttConfigs := make([]voice.STTProviderConfig, len(cfg.Voice.STT))
			for i, p := range cfg.Voice.STT {
				sttConfigs[i] = voice.STTProviderConfig{
					Provider: p.Provider,
					APIKey:   p.APIKey,
					Model:    p.Model,
					URL:      p.URL,
				}
			}
			sttClient, err := voice.NewSTTChain(sttConfigs, logger)
			if err != nil {
				logger.Error("voice: STT初期化失敗", "error", err)
			} else {
				ttsConfigs := make([]voice.TTSProviderConfig, len(cfg.Voice.TTS))
				for i, p := range cfg.Voice.TTS {
					ttsConfigs[i] = voice.TTSProviderConfig{
						Provider:  p.Provider,
						URL:       p.URL,
						SpeakerID: p.SpeakerID,
						Model:     p.Model,
						Style:     p.Style,
					}
				}
				ttsClient, ttsErr := voice.NewTTSChain(ttsConfigs, logger)
				if ttsErr != nil {
					logger.Error("voice: TTS初期化失敗", "error", ttsErr)
				} else {
					dc.SetupVoice(sttClient, ttsClient)
					if vp := dc.VoicePipeline(); vp != nil {
						registry.Register(discord.NewVoiceJoin(vp, s, cfg.Voice.AllowedChannels, logger))
						registry.Register(discord.NewVoiceLeave(vp, s, logger))
						if ds, ok := ag.GetSession(agent.SourceKeyDiscord).(*agent.DiscordSession); ok {
							ds.SetVoice(vp)
						}
						logger.Info("voice tools registered")
					}
				}
			}
		}

		// Fetch bot's own identity from Discord and register in Users.
		me := s.State.User
		userStore.AddBotID(me.ID)
		ag.SetBotID(me.ID)

		botUser, err := userStore.Resolve(context.Background(), "discord", me.ID, me.Username)
		if err != nil {
			logger.Warn("failed to resolve bot user", "error", err)
		} else {
			name := me.Username
			if me.GlobalName != "" {
				name = me.GlobalName
			}
			if botUser.DisplayName != name {
				if err := userStore.UpdateDisplayName(context.Background(), botUser.ID, name); err != nil {
					logger.Warn("failed to update bot display name", "error", err)
				}
			}
			logger.Info("bot identity registered", "user_id", botUser.ID, "name", name, "is_bot", botUser.IsBot)
		}
	})
}

func startInternalHTTP(injector do.Injector, cfgPath string) {
	cfg := do.MustInvoke[*config.Config](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	logRing := do.MustInvoke[*observe.RingBuffer](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	channelStore := do.MustInvoke[*channel.Store](injector)
	sched := do.MustInvoke[*scheduler.Scheduler](injector)

	mux := http.NewServeMux()
	mux.Handle("/internal/logs", observe.LogHandler(logRing))
	mux.HandleFunc("POST /internal/compact", func(w http.ResponseWriter, r *http.Request) {
		compactCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ag.ForceCompact(compactCtx)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"message_count":%d}`, ag.AgentContext().Len())
	})
	mux.HandleFunc("POST /internal/reload-prompt", func(w http.ResponseWriter, r *http.Request) {
		dir := cfg.Agent.PromptDir
		if dir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(cfgPath), dir)
		}
		var parts []string
		for _, name := range []string{"IDENTITY.md", "SOUL.md"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				logger.Error("reload-prompt read", "name", name, "error", err)
				http.Error(w, `{"error":"read failed"}`, http.StatusInternalServerError)
				return
			}
			parts = append(parts, strings.TrimSpace(string(data)))
		}
		newPrompt := strings.Join(parts, "\n\n")
		ag.ReloadPrompt(newPrompt)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"length":%d}`, len(newPrompt))
	})
	mux.HandleFunc("POST /internal/reload-channel-settings", func(w http.ResponseWriter, r *http.Request) {
		if err := channelStore.Reload(r.Context()); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("GET /internal/identity", func(w http.ResponseWriter, r *http.Request) {
		botPlatformID := ag.BotID()
		result := map[string]any{"bot_platform_id": botPlatformID}
		// Resolve bot's internal user record if available.
		if botPlatformID != "" {
			userStore := do.MustInvoke[*user.SQLiteStore](injector)
			if u, err := userStore.Resolve(r.Context(), "discord", botPlatformID, ""); err == nil {
				result["bot_user_id"] = u.ID
				result["bot_name"] = u.DisplayName
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /internal/context", func(w http.ResponseWriter, r *http.Request) {
		actx := ag.AgentContext()
		msgs := actx.MessagesWithSystem()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"messages":         msgs,
			"count":            len(msgs),
			"estimated_tokens": actx.EstimatedTokens(),
			"usage_ratio":      actx.UsageRatio(),
			"max_tokens":       actx.MaxTokens(),
			"ephemeral":        ag.LastEphemeral(),
		})
	})
	mux.HandleFunc("GET /internal/scheduler/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sched == nil {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": sched.ListJobs()})
	})
	mux.HandleFunc("POST /internal/trigger/{task}", func(w http.ResponseWriter, r *http.Request) {
		if sched == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "scheduler not enabled"})
			return
		}

		taskName := r.PathValue("task")
		var reqBody struct {
			Config map[string]any `json:"config"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		var taskCfg json.RawMessage
		if reqBody.Config != nil {
			taskCfg, _ = json.Marshal(reqBody.Config)
		} else {
			for _, j := range cfg.Consolidator.Scheduler.Jobs {
				if j.Task == taskName {
					taskCfg, _ = json.Marshal(j.Config)
					break
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := sched.TriggerTask(r.Context(), taskName, taskCfg); err != nil {
			logger.Error("trigger: task failed", "task", taskName, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	// LLM provider info / swap (persisted in app_settings).
	llmClient := do.MustInvoke[*llm.Client](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")

	// Restore saved provider on startup.
	{
		var savedJSON string
		err := llmDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'llm_provider'`).Scan(&savedJSON)
		if err == nil && savedJSON != "" {
			var saved struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				APIKey   string `json:"api_key"`
				APIBase  string `json:"api_base"`
				MaxCtx   int    `json:"max_ctx"`
				Vision   bool   `json:"vision"`
			}
			if json.Unmarshal([]byte(savedJSON), &saved) == nil && saved.Provider != "" {
				if swapErr := llmClient.SwapProvider(saved.Provider, saved.Model, saved.APIKey, saved.APIBase, saved.MaxCtx, saved.Vision); swapErr != nil {
					logger.Warn("failed to restore saved LLM provider", "error", swapErr)
				} else {
					if saved.MaxCtx > 0 {
						ag.AgentContext().SetMaxTokens(saved.MaxCtx)
					}
					logger.Info("restored saved LLM provider", "provider", saved.Provider, "model", saved.Model, "max_ctx", saved.MaxCtx)
				}
			}
		}
	}

	mux.HandleFunc("GET /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		prov, model, apiBase, vision := llmClient.ProviderInfo()
		presets := make([]map[string]any, len(cfg.LLM.Presets))
		for i, p := range cfg.LLM.Presets {
			presets[i] = map[string]any{
				"name":       p.Name,
				"provider":   p.Provider,
				"model":      p.Model,
				"api_base":   p.APIBase,
				"max_tokens": p.MaxTokens,
				"vision":     p.Vision,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"provider": prov,
			"model":    model,
			"api_base": apiBase,
			"max_ctx":  llmClient.MaxContextTokens(),
			"vision":   vision,
			"presets":  presets,
		})
	})
	mux.HandleFunc("PUT /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Preset   string `json:"preset"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			APIKey   string `json:"api_key"`
			APIBase  string `json:"api_base"`
			MaxCtx   int    `json:"max_ctx"`
			Vision   bool   `json:"vision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		// Resolve preset if specified.
		if body.Preset != "" {
			p := cfg.LLM.FindPreset(body.Preset)
			if p == nil {
				http.Error(w, fmt.Sprintf(`{"error":"preset %q not found"}`, body.Preset), http.StatusBadRequest)
				return
			}
			body.Provider = p.Provider
			body.Model = p.Model
			body.APIBase = p.APIBase
			body.Vision = p.Vision
			if p.APIKey != "" {
				body.APIKey = p.APIKey
			}
			if p.MaxTokens > 0 {
				body.MaxCtx = p.MaxTokens
			}
		}
		if body.Provider == "" || body.Model == "" {
			http.Error(w, `{"error":"provider and model required"}`, http.StatusBadRequest)
			return
		}
		// Resolve well-known API base per provider.
		if body.APIBase == "" {
			switch body.Provider {
			case "openai":
				body.APIBase = "https://api.openai.com/v1"
			case "zhipu":
				body.APIBase = "https://open.bigmodel.cn/api/paas/v4"
			case "qwen":
				body.APIBase = "https://dashscope.aliyuncs.com/compatible-mode/v1"
			}
		}
		// Resolve API key: first try preset with exact provider+api_base match,
		// then try provider-only match, then fall back to config default
		// only when the provider matches.
		if body.APIKey == "" {
			// Exact match: provider AND api_base.
			for _, p := range cfg.LLM.Presets {
				if p.Provider == body.Provider && p.APIBase == body.APIBase && p.APIKey != "" {
					body.APIKey = p.APIKey
					break
				}
			}
		}
		if body.APIKey == "" {
			// Loose match: provider only.
			for _, p := range cfg.LLM.Presets {
				if p.Provider == body.Provider && p.APIKey != "" {
					body.APIKey = p.APIKey
					break
				}
			}
		}
		if body.APIKey == "" && body.Provider == cfg.LLM.Provider {
			body.APIKey = cfg.LLM.APIKey
		}
		if body.MaxCtx <= 0 {
			body.MaxCtx = cfg.LLM.MaxTokens
		}
		if err := llmClient.SwapProvider(body.Provider, body.Model, body.APIKey, body.APIBase, body.MaxCtx, body.Vision); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		ag.AgentContext().SetMaxTokens(body.MaxCtx)
		// Force compaction if context exceeds new limit.
		if ag.AgentContext().UsageRatio() > 0.5 {
			compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			ag.ForceCompact(compactCtx)
			compactCancel()
			logger.Info("context compacted after provider switch", "messages", ag.AgentContext().Len(), "max_ctx", body.MaxCtx)
		} else {
			logger.Info("context max tokens updated", "max_ctx", body.MaxCtx)
		}
		// Persist selection.
		settingsJSON, _ := json.Marshal(body)
		_, _ = llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('llm_provider', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			string(settingsJSON),
		)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// Playground: chat with LLM using current context snapshot (read-only).
	// Tool registry listing + enable/disable.
	registry := do.MustInvoke[*tool.Registry](injector)

	// Restore disabled tools on startup.
	{
		var disabledJSON string
		err := llmDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'disabled_tools'`).Scan(&disabledJSON)
		if err == nil && disabledJSON != "" {
			var names []string
			if json.Unmarshal([]byte(disabledJSON), &names) == nil && len(names) > 0 {
				registry.SetDisabled(names)
				logger.Info("restored disabled tools", "count", len(names))
			}
		}
	}

	saveDisabledTools := func() {
		names := registry.DisabledNames()
		data, _ := json.Marshal(names)
		_, _ = llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('disabled_tools', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			string(data),
		)
	}

	mux.HandleFunc("GET /internal/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := registry.All()
		type toolInfo struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Enabled     bool            `json:"enabled"`
		}
		out := make([]toolInfo, 0, len(tools))
		for _, t := range tools {
			out = append(out, toolInfo{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
				Enabled:     !registry.IsDisabled(t.Name()),
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": out})
	})
	mux.HandleFunc("PUT /internal/tools/{name}/enabled", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		// Update the disabled set.
		current := registry.DisabledNames()
		var updated []string
		if body.Enabled {
			for _, n := range current {
				if n != name {
					updated = append(updated, n)
				}
			}
		} else {
			found := false
			for _, n := range current {
				updated = append(updated, n)
				if n == name {
					found = true
				}
			}
			if !found {
				updated = append(updated, name)
			}
		}
		registry.SetDisabled(updated)
		saveDisabledTools()
		logger.Info("tool toggled", "tool", name, "enabled", body.Enabled)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// VOICEVOX speaker management (find voicevox config from TTS providers).
	var voicevoxCfg *config.TTSProvider
	for i := range cfg.Voice.TTS {
		if cfg.Voice.TTS[i].Provider == "voicevox" {
			voicevoxCfg = &cfg.Voice.TTS[i]
			break
		}
	}
	if cfg.Voice.Enabled && voicevoxCfg != nil && voicevoxCfg.URL != "" {
		voicevoxURL := voicevoxCfg.URL
		mux.HandleFunc("GET /internal/voicevox/speakers", func(w http.ResponseWriter, r *http.Request) {
			resp, err := http.Get(voicevoxURL + "/speakers")
			if err != nil {
				http.Error(w, `{"error":"voicevox unreachable"}`, http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			io.Copy(w, resp.Body)
		})
		mux.HandleFunc("GET /internal/voicevox/speaker", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"speaker_id": voicevoxCfg.SpeakerID})
		})
		mux.HandleFunc("PUT /internal/voicevox/speaker", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				SpeakerID int `json:"speaker_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			voicevoxCfg.SpeakerID = body.SpeakerID
			dc := do.MustInvoke[*discord.Chat](injector)
			if dc != nil {
				if vp := dc.VoicePipeline(); vp != nil {
					vp.SetSpeakerID(body.SpeakerID)
				}
			}
			logger.Info("voicevox: speaker変更", "speaker_id", body.SpeakerID)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		})
	}

	// Physical device (ESP32) WebSocket endpoint.
	{
		bus := do.MustInvoke[*event.Bus](injector)
		var ttsClient voice.TTS
		if cfg.Voice.Enabled && len(cfg.Voice.TTS) > 0 {
			deviceTTSConfigs := make([]voice.TTSProviderConfig, len(cfg.Voice.TTS))
			for i, p := range cfg.Voice.TTS {
				deviceTTSConfigs[i] = voice.TTSProviderConfig{
					Provider:  p.Provider,
					URL:       p.URL,
					SpeakerID: p.SpeakerID,
					Model:     p.Model,
					Style:     p.Style,
				}
			}
			ttsClient, _ = voice.NewTTSChain(deviceTTSConfigs, logger)
		}
		yoloURL := os.Getenv("YOLO_URL")
		if yoloURL == "" {
			yoloURL = "http://yolo:8002"
		}
		// Look up home channel from DB.
		var deviceChannel string
		db := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
		_ = db.QueryRow("SELECT channel_id FROM channel_settings WHERE home = 1 LIMIT 1").Scan(&deviceChannel)
		var sttClient voice.STT
		if cfg.Voice.Enabled && len(cfg.Voice.STT) > 0 {
			sttClient, _ = voice.NewSTT(voice.STTProviderConfig{
				Provider: cfg.Voice.STT[0].Provider,
				APIKey:   cfg.Voice.STT[0].APIKey,
				Model:    cfg.Voice.STT[0].Model,
				URL:      cfg.Voice.STT[0].URL,
			})
		}
		// Look up owner from DB
		var ownerID, ownerName string
		_ = db.QueryRow("SELECT id, display_name FROM users WHERE role = 'owner' LIMIT 1").Scan(&ownerID, &ownerName)
		if ownerID == "" {
			ownerID = "owner"
			ownerName = "オーナー"
		}
		hub := device.NewHub(bus, ttsClient, sttClient, yoloURL, deviceChannel, ownerID, ownerName, logger)
		mux.HandleFunc("GET /ws/device", hub.Handler())
		mux.HandleFunc("GET /internal/device/frame", hub.Frames().FrameHandler())
		mux.HandleFunc("GET /internal/device/detections", hub.Frames().DetectionStreamHandler())
		ag.SetSession(agent.SourceKeyDevice, agent.NewDeviceSession(
			ag.AgentContextFor(agent.SourceKeyDevice), hub, logger,
		))
		registry.Register(device.NewServoTool(hub))
		registry.Register(device.NewCaptureTool(hub))
		registry.Register(device.NewFaceTool(hub))
		registry.Register(device.NewLookTool(hub, do.MustInvoke[*llm.Client](injector)))
		mux.HandleFunc("GET /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"enabled": hub.ChangeDetector().Enabled()})
		})
		mux.HandleFunc("PUT /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			hub.ChangeDetector().SetEnabled(body.Enabled)
			logger.Info("device: 視界変化検出の切り替え", "enabled", body.Enabled)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		})
		mux.HandleFunc("POST /internal/device/servo", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Pan  int `json:"pan"`
				Tilt int `json:"tilt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			dev := hub.Device()
			if dev == nil {
				http.Error(w, `{"error":"device not connected"}`, http.StatusServiceUnavailable)
				return
			}
			if err := dev.SendCommand(map[string]any{"cmd": "servo", "pan": body.Pan, "tilt": body.Tilt}); err != nil {
				http.Error(w, `{"error":"send failed"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"pan":%d,"tilt":%d}`, body.Pan, body.Tilt)
		})

		mux.HandleFunc("PUT /internal/device/volume", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Level int `json:"level"` // 0-100
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			dev := hub.Device()
			if dev == nil {
				http.Error(w, `{"error":"device not connected"}`, http.StatusServiceUnavailable)
				return
			}
			if err := dev.SendCommand(map[string]any{"cmd": "volume", "level": body.Level}); err != nil {
				http.Error(w, `{"error":"send failed"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"level":%d}`, body.Level)
		})

		// Start periodic capture loop (333ms = ~3fps).
		captureCtx, _ := context.WithCancel(context.Background())
		hub.StartCaptureLoop(captureCtx, 333)
		logger.Info("デバイス接続口を開いた")
	}

	// Overland location tracking endpoint.
	locStore := do.MustInvoke[*location.Store](injector)
	if locStore != nil {
		locHandler := location.NewHandler(locStore, cfg.Location.Token, logger)
		mux.Handle("POST /internal/overland", locHandler)
		mux.HandleFunc("POST /internal/reload-location-settings", func(w http.ResponseWriter, r *http.Request) {
			if err := locStore.LoadSettings(r.Context()); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		})
		logger.Info("overland location endpoint enabled")
	}

	logger.Info("internal server starting", "addr", cfg.Observe.MetricsAddr)
	if err := http.ListenAndServe(cfg.Observe.MetricsAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
