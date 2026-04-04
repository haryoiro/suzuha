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
	"github.com/haryoiro/suzuha/external/stt"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/device"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/cmd/suzuha-agent/langfuse"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/location"
	"github.com/haryoiro/suzuha/cmd/suzuha-agent/mcp"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/cmd/suzuha-agent/observe"
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
	store := do.MustInvoke[*memory.SQLiteStore](injector)
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

	// Register Discord OnReady callback.
	dc := do.MustInvoke[*discord.Chat](injector)
	if dc != nil {
		registerDiscordOnReady(injector, dc)
	}

	// Start internal HTTP server.
	if cfg.Observe.InternalAddr != "" {
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

	// LLM provider / preset management.
	llmClient := do.MustInvoke[*llm.Client](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
	presetStore := do.MustInvoke[*llm.PresetStore](injector)

	// Restore role assignments from DB on startup.
	{
		assignments, err := presetStore.Assignments(context.Background())
		if err == nil {
			for role, presetName := range assignments {
				p, err := presetStore.Get(context.Background(), presetName)
				if err != nil {
					logger.Warn("プリセットの取得に失敗", "role", role, "preset", presetName, "error", err)
					continue
				}
				if err := llmClient.SwapRole(role, *p); err != nil {
					logger.Warn("ロールの復元に失敗", "role", role, "error", err)
				} else {
					if role == "conversation" && p.MaxTokens > 0 {
						ag.AgentContext().SetMaxTokens(p.MaxTokens)
					}
					logger.Info("LLMロールを復元", "role", role, "preset", presetName)
				}
			}
		}
	}

	// --- Preset CRUD ---

	mux.HandleFunc("GET /internal/llm/presets", func(w http.ResponseWriter, r *http.Request) {
		presets, err := presetStore.List(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(presets)
	})

	mux.HandleFunc("POST /internal/llm/presets", func(w http.ResponseWriter, r *http.Request) {
		var p llm.Preset
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if p.Name == "" || p.Provider == "" || p.Model == "" {
			http.Error(w, `{"error":"name, provider, model required"}`, http.StatusBadRequest)
			return
		}
		if len(p.Capabilities) == 0 {
			p.Capabilities = []string{"text"}
		}
		if p.Source == "" {
			p.Source = "user"
		}
		if err := presetStore.Save(r.Context(), &p); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	mux.HandleFunc("PUT /internal/llm/presets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var p llm.Preset
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		p.Name = name
		if p.Provider == "" || p.Model == "" {
			http.Error(w, `{"error":"provider and model required"}`, http.StatusBadRequest)
			return
		}
		if len(p.Capabilities) == 0 {
			p.Capabilities = []string{"text"}
		}
		if err := presetStore.Save(r.Context(), &p); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	mux.HandleFunc("DELETE /internal/llm/presets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := presetStore.Delete(r.Context(), name); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// --- Role assignments ---

	mux.HandleFunc("GET /internal/llm/assignments", func(w http.ResponseWriter, r *http.Request) {
		assignments, err := presetStore.Assignments(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(assignments)
	})

	mux.HandleFunc("PUT /internal/llm/assignments/{role}", func(w http.ResponseWriter, r *http.Request) {
		role := r.PathValue("role")
		var body struct {
			Preset string `json:"preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Preset == "" {
			http.Error(w, `{"error":"preset required"}`, http.StatusBadRequest)
			return
		}

		// プリセットを取得して割り当て
		p, err := presetStore.Get(r.Context(), body.Preset)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		if err := presetStore.Assign(r.Context(), role, body.Preset); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		// Client のロールを切り替え
		if err := llmClient.SwapRole(role, *p); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}

		// conversation ロールの場合はコンテキスト調整
		if role == "conversation" && p.MaxTokens > 0 {
			ag.AgentContext().SetMaxTokens(p.MaxTokens)
			if ag.AgentContext().UsageRatio() > 0.5 {
				compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				ag.ForceCompact(compactCtx)
				compactCancel()
				logger.Info("context compacted after role switch", "role", role, "max_ctx", p.MaxTokens)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// --- 後方互換: GET/PUT /internal/llm ---

	mux.HandleFunc("GET /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		prov, model, apiBase, vision := llmClient.ProviderInfo()
		presets, _ := presetStore.List(r.Context())
		assignments, _ := presetStore.Assignments(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"provider":    prov,
			"model":       model,
			"api_base":    apiBase,
			"max_ctx":     llmClient.MaxContextTokens(),
			"vision":      vision,
			"presets":     presets,
			"assignments": assignments,
		})
	})

	mux.HandleFunc("PUT /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Preset string `json:"preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Preset == "" {
			http.Error(w, `{"error":"preset required"}`, http.StatusBadRequest)
			return
		}

		p, err := presetStore.Get(r.Context(), body.Preset)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"preset %q not found"}`, body.Preset), http.StatusBadRequest)
			return
		}

		if err := presetStore.Assign(r.Context(), "conversation", body.Preset); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
			return
		}
		if err := llmClient.SwapRole("conversation", *p); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}

		if p.MaxTokens > 0 {
			ag.AgentContext().SetMaxTokens(p.MaxTokens)
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
			ttsClient, _ = tts.NewTTSChain(deviceTTSConfigs, logger)
		}
		yoloURL := os.Getenv("YOLO_URL")
		if yoloURL == "" {
			yoloURL = "http://yolo:8002"
		}
		// Look up home channel from DB.
		var deviceChannel string
		db := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
		_ = db.QueryRow("SELECT channel_id FROM channel_settings WHERE home = 1 LIMIT 1").Scan(&deviceChannel)
		var sttClient stt.STT
		if cfg.Voice.Enabled && len(cfg.Voice.STT) > 0 {
			sttClient, _ = stt.NewSTT(stt.STTProviderConfig{
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
		mux.HandleFunc("GET /ws/web", hub.WebHandler())
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

		// Object tracker API
		mux.HandleFunc("GET /internal/device/tracker", func(w http.ResponseWriter, r *http.Request) {
			tr := hub.Tracker()
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
			tr := hub.Tracker()
			if body.Enabled != nil {
				tr.SetEnabled(*body.Enabled)
				logger.Info("device: トラッカー切り替え", "enabled", *body.Enabled)
			}
			if body.TargetLabel != nil {
				tr.SetTargetLabel(*body.TargetLabel)
			}
			tr.ApplyPartial(device.TrackerPatch{
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

	logger.Info("internal server starting", "addr", cfg.Observe.InternalAddr)
	if err := http.ListenAndServe(cfg.Observe.InternalAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
