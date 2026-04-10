package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	"github.com/haryoiro/suzuha/internal/gateway"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
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

	// Delegated handler groups.
	registerLLMHandlers(mux, injector, ag, logger)
	registerToolHandlers(mux, injector, logger)

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

	// Device handlers.
	captureCancel := registerDeviceHandlers(mux, injector, ag, gw)
	defer captureCancel()

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
