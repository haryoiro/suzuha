package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/haryoiro/suzuha/external/stt"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/adapter/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/adapter/device"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/feature/vision"
	"github.com/haryoiro/suzuha/internal/gateway"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/feature/location"
	"github.com/haryoiro/suzuha/internal/mcp"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/user"
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
	store := do.MustInvoke[memory.Backend](injector)
	defer store.Close()

	mcpMgr := do.MustInvoke[*mcp.Manager](injector)
	defer mcpMgr.Close()

	// Langfuse TracerProvider shutdown (flush pending spans).
	lfTP := do.MustInvoke[*langfuse.TracerProvider](injector)
	if lfTP != nil {
		defer lfTP.Shutdown(context.Background())
	}

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

	// Create Gateway early so startInternalHTTP can register Device source.
	gw := gateway.New(logger)

	// Register Discord OnReady callback.
	dc := do.MustInvoke[*discord.Chat](injector)
	if dc != nil {
		registerDiscordOnReady(injector, dc)
		gw.Register(dc)
	} else {
		gw.Register(chatIface.(gateway.Source))
		ag.SetSession(agent.SourceKeyCLI, agent.NewCLISession(
			ag.AgentContextFor(agent.SourceKeyCLI),
			os.Stdout,
			logger,
		))
	}

	// Start internal HTTP server.
	if cfg.Observe.InternalAddr != "" {
		go startInternalHTTP(injector, cfgPath, gw)
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

	// Start Gateway (manages all source lifecycles).
	go func() {
		if err := gw.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("gateway stopped", "error", err)
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


		// Voice chat setup.
		if cfg.Voice.Enabled {
			sttConfigs := make([]stt.STTProviderConfig, len(cfg.Voice.STT))
			for i, p := range cfg.Voice.STT {
				sttConfigs[i] = stt.STTProviderConfig{
					Provider: p.Provider,
					APIKey:   p.APIKey,
					Model:    p.Model,
					URL:      p.URL,
				}
			}
			sttClient, err := stt.NewSTTChain(sttConfigs, logger)
			if err != nil {
				logger.Error("voice: STT初期化失敗", "error", err)
			} else {
				ttsConfigs := make([]tts.TTSProviderConfig, len(cfg.Voice.TTS))
				for i, p := range cfg.Voice.TTS {
					ttsConfigs[i] = tts.TTSProviderConfig{
						Provider:  p.Provider,
						URL:       p.URL,
						SpeakerID: p.SpeakerID,
						Model:     p.Model,
						Style:     p.Style,
					}
				}
				ttsClient, ttsErr := tts.NewTTSChain(ttsConfigs, logger)
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

func startInternalHTTP(injector do.Injector, cfgPath string, gw *gateway.Gateway) {
	cfg := do.MustInvoke[*config.Config](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	logRing := do.MustInvoke[*observe.RingBuffer](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	channelStore := do.MustInvoke[*channel.Store](injector)
	sched := do.MustInvoke[*scheduler.Scheduler](injector)

	mux := http.NewServeMux()
	mux.Handle("/internal/logs", observe.LogHandler(logRing))
	mux.Handle("GET /internal/gateway/status", gw.StatusHandler())
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
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, `{"ok":false,"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}

		var taskCfg json.RawMessage
		if reqBody.Config != nil {
			var err error
			taskCfg, err = json.Marshal(reqBody.Config)
			if err != nil {
				logger.Error("trigger: config marshal に失敗", "task", taskName, "error", err)
				http.Error(w, `{"ok":false,"error":"config marshal failed"}`, http.StatusInternalServerError)
				return
			}
		} else {
			for _, j := range cfg.Consolidator.Scheduler.Jobs {
				if j.Task == taskName {
					var err error
					taskCfg, err = json.Marshal(j.Config)
					if err != nil {
						logger.Error("trigger: job config marshal に失敗", "task", taskName, "error", err)
						http.Error(w, `{"ok":false,"error":"config marshal failed"}`, http.StatusInternalServerError)
						return
					}
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

	// LLM provider / model / role management (3層分離).
	llmClient := do.MustInvoke[*llm.Client](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
	providerRegistry := do.MustInvoke[*llm.ProviderRegistry](injector)

	// Restore role assignments from DB on startup.
	{
		assignments, err := providerRegistry.Assignments(context.Background())
		if err == nil {
			for _, a := range assignments {
				spec, err := providerRegistry.BuildRoleSpec(context.Background(), a.ProviderName, a.ModelID)
				if err != nil {
					logger.Warn("ロール復元: RoleSpec 構築失敗", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID, "error", err)
					continue
				}
				llmClient.SwapRoleSpec(a.Role, *spec)
				if a.Role == "conversation" && spec.MaxContext > 0 {
					ag.AgentContext().SetMaxTokens(spec.MaxContext)
				}
				logger.Info("LLMロールを復元", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID)
			}
		}
	}

	// GET /internal/llm — ステータス概要
	mux.HandleFunc("GET /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		prov, model, apiBase, vision := llmClient.ProviderInfo()
		assignments, _ := providerRegistry.Assignments(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"provider":    prov,
			"model":       model,
			"api_base":    apiBase,
			"max_ctx":     llmClient.MaxContextTokens(),
			"vision":      vision,
			"assignments": assignments,
		})
	})

	mux.HandleFunc("GET /internal/llm/providers", func(w http.ResponseWriter, r *http.Request) {
		providers, err := providerRegistry.ListProviders(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(providers)
	})

	mux.HandleFunc("GET /internal/llm/models", func(w http.ResponseWriter, r *http.Request) {
		providerFilter := r.URL.Query().Get("provider")
		models, err := providerRegistry.ListModels(r.Context(), providerFilter)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)
	})

	mux.HandleFunc("POST /internal/llm/models", func(w http.ResponseWriter, r *http.Request) {
		var m llm.ModelInfo
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if m.ProviderName == "" || m.ModelID == "" {
			http.Error(w, `{"error":"provider_name and model_id required"}`, http.StatusBadRequest)
			return
		}
		if len(m.Capabilities) == 0 {
			m.Capabilities = []string{"text"}
		}
		if err := providerRegistry.SaveModel(r.Context(), &m); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	mux.HandleFunc("POST /internal/llm/models/refresh", func(w http.ResponseWriter, r *http.Request) {
		providers, err := providerRegistry.ListProviders(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		var total int
		for _, p := range providers {
			meta := llm.GetProviderMeta(p.Type)
			if meta == nil {
				continue
			}
			models, err := meta.ListModels(r.Context(), p.APIKey, p.APIBase)
			if err != nil {
				logger.Warn("モデルカタログ更新失敗", "provider", p.Name, "error", err)
				continue
			}
			for i := range models {
				models[i].ProviderName = p.Name
				if err := providerRegistry.SaveModel(r.Context(), &models[i]); err != nil {
					logger.Warn("モデル保存失敗", "provider", p.Name, "model", models[i].ModelID, "error", err)
				} else {
					total++
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "models_updated": total})
	})

	mux.HandleFunc("GET /internal/llm/roles", func(w http.ResponseWriter, r *http.Request) {
		assignments, err := providerRegistry.Assignments(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(assignments)
	})

	mux.HandleFunc("PUT /internal/llm/roles/{role}", func(w http.ResponseWriter, r *http.Request) {
		role := r.PathValue("role")
		var body struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" || body.Model == "" {
			http.Error(w, `{"error":"provider and model required"}`, http.StatusBadRequest)
			return
		}

		spec, err := providerRegistry.BuildRoleSpec(r.Context(), body.Provider, body.Model)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}

		if err := providerRegistry.AssignRole(r.Context(), role, body.Provider, body.Model); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		llmClient.SwapRoleSpec(role, *spec)

		if role == "conversation" && spec.MaxContext > 0 {
			ag.AgentContext().SetMaxTokens(spec.MaxContext)
			if ag.AgentContext().UsageRatio() > 0.5 {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				ag.ForceCompact(compactCtx)
				compactCancel()
			}
		}

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
		data, err := json.Marshal(names)
		if err != nil {
			logger.Error("disabled tools の marshal に失敗", "error", err)
			return
		}
		if _, err := llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('disabled_tools', $1) ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`,
			string(data),
		); err != nil {
			logger.Error("disabled tools の保存に失敗", "error", err)
		}
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

	// Tool execution API.
	mux.HandleFunc("POST /internal/tools/{name}/execute", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		t, ok := registry.Get(name)
		if !ok {
			http.Error(w, `{"error":"tool not found"}`, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if len(body) == 0 {
			body = []byte("{}")
		}
		logger.Info("tool: 手動実行", "tool", name)
		result, err := t.Execute(r.Context(), json.RawMessage(body))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		var text string
		for _, c := range result.Content {
			text += c.Text
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":       !result.IsError,
			"output":   text,
			"is_error": result.IsError,
		})
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
		var ttsClient tts.TTS
		if cfg.Voice.Enabled && len(cfg.Voice.TTS) > 0 {
			deviceTTSConfigs := make([]tts.TTSProviderConfig, len(cfg.Voice.TTS))
			for i, p := range cfg.Voice.TTS {
				deviceTTSConfigs[i] = tts.TTSProviderConfig{
					Provider:  p.Provider,
					URL:       p.URL,
					SpeakerID: p.SpeakerID,
					Model:     p.Model,
					Style:     p.Style,
				}
			}
			var err error
			ttsClient, err = tts.NewTTSChain(deviceTTSConfigs, logger)
			if err != nil {
				logger.Error("TTS クライアントの初期化に失敗", "error", err)
			}
		}
		yoloURL := os.Getenv("YOLO_URL")
		if yoloURL == "" {
			yoloURL = "http://yolo:8002"
		}
		// Look up home channel from DB.
		var deviceChannel string
		db := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
		if err := db.QueryRow("SELECT channel_id FROM channel_settings WHERE home = true LIMIT 1").Scan(&deviceChannel); err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.Error("ホームチャンネルの取得に失敗", "error", err)
		}
		var sttClient stt.STT
		if cfg.Voice.Enabled && len(cfg.Voice.STT) > 0 {
			var err error
			sttClient, err = stt.NewSTT(stt.STTProviderConfig{
				Provider: cfg.Voice.STT[0].Provider,
				APIKey:   cfg.Voice.STT[0].APIKey,
				Model:    cfg.Voice.STT[0].Model,
				URL:      cfg.Voice.STT[0].URL,
			})
			if err != nil {
				logger.Error("STT クライアントの初期化に失敗", "error", err)
			}
		}
		// Look up owner from DB
		var ownerID, ownerName string
		if err := db.QueryRow("SELECT id, display_name FROM users WHERE role = 'owner' LIMIT 1").Scan(&ownerID, &ownerName); err != nil && !errors.Is(err, sql.ErrNoRows) {
			logger.Error("オーナー情報の取得に失敗", "error", err)
		}
		if ownerID == "" {
			ownerID = "owner"
			ownerName = "オーナー"
		}
		hub := device.NewHub(bus, ttsClient, sttClient, ownerID, ownerName, logger)
		devAdapter := device.NewDeviceAdapter(hub)
		visionFeature := vision.New(bus, yoloURL, deviceChannel, devAdapter, devAdapter,
			do.MustInvoke[*llm.Client](injector), logger)
		hub.SetImageHandler(visionFeature.Pipeline())
		do.ProvideValue(injector, visionFeature)
		mux.HandleFunc("GET /ws/device", hub.Handler())
		mux.HandleFunc("GET /ws/web", hub.WebHandler())
		mux.HandleFunc("GET /internal/device/frame", visionFeature.Frames().FrameHandler())
		mux.HandleFunc("GET /internal/device/detections", visionFeature.Frames().DetectionStreamHandler())
		ag.SetSession(agent.SourceKeyDevice, agent.NewDeviceSession(
			ag.AgentContextFor(agent.SourceKeyDevice), hub, logger,
		))
		gw.Register(device.NewSource(hub))

		mux.HandleFunc("GET /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"enabled": visionFeature.ChangeDetector().Enabled()})
		})
		mux.HandleFunc("PUT /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			visionFeature.ChangeDetector().SetEnabled(body.Enabled)
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

		// Object tracker API
		mux.HandleFunc("GET /internal/device/tracker", func(w http.ResponseWriter, r *http.Request) {
			tr := visionFeature.Tracker()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"enabled": tr.Enabled(),
				"config":  tr.Config(),
			})
		})
		mux.HandleFunc("PUT /internal/device/tracker", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Enabled          *bool    `json:"enabled"`
				TargetLabel      *string  `json:"target_label"`
				DeadZone         *float64 `json:"dead_zone"`
				SmoothingAlpha   *float64 `json:"smoothing_alpha"`
				ProportionalGain *float64 `json:"proportional_gain"`
				MaxDegPerFrame   *float64 `json:"max_deg_per_frame"`
				InvertPan        *bool    `json:"invert_pan"`
				InvertTilt       *bool    `json:"invert_tilt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
				return
			}
			tr := visionFeature.Tracker()
			if body.Enabled != nil {
				tr.SetEnabled(*body.Enabled)
				logger.Info("device: トラッカー切り替え", "enabled", *body.Enabled)
			}
			if body.TargetLabel != nil {
				tr.SetTargetLabel(*body.TargetLabel)
			}
			tr.ApplyPartial(vision.TrackerPatch{
				DeadZone:         body.DeadZone,
				SmoothingAlpha:   body.SmoothingAlpha,
				ProportionalGain: body.ProportionalGain,
				MaxDegPerFrame:   body.MaxDegPerFrame,
				InvertPan:        body.InvertPan,
				InvertTilt:       body.InvertTilt,
			})
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		})

		// Start periodic capture loop (333ms = ~3fps).
		captureCtx, captureCancel := context.WithCancel(context.Background())
		defer captureCancel()
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

	logger.Info("internal server starting", "addr", cfg.Observe.InternalAddr)
	if err := http.ListenAndServe(cfg.Observe.InternalAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
