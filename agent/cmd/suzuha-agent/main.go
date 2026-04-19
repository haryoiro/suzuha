package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/external/stt"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/api/control"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/adapter/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/adapter/device"
	"github.com/haryoiro/suzuha/internal/di"
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
	injector := do.New(di.Packages(cfgPath)...)

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
	userStore := do.MustInvoke[user.Store](injector)
	botReg := do.MustInvoke[user.BotRegistrar](injector)
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
		botReg.AddBotID(me.ID)
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

	controlHandler := do.MustInvoke[gen.Handler](injector)
	controlOgen, err := control.NewOgenHandler(controlHandler)
	if err != nil {
		logger.Error("control API の初期化に失敗", "error", err)
		return
	}

	mux := http.NewServeMux()
	mux.Handle("/internal/logs", observe.LogHandler(logRing))
	mux.Handle("GET /internal/gateway/status", gw.StatusHandler())
	// Ogen-backed control API (段階的に /internal/* を移行中).
	mux.Handle("POST /internal/reload-channel-settings", controlOgen)
	mux.Handle("GET /internal/identity", controlOgen)
	mux.Handle("GET /internal/context", controlOgen)
	mux.Handle("POST /internal/compact", controlOgen)
	mux.Handle("POST /internal/reload-prompt", controlOgen)
	mux.Handle("GET /internal/scheduler/jobs", controlOgen)
	mux.Handle("POST /internal/trigger/{task}", controlOgen)

	// LLM provider / model / role management (3層分離).
	llmClient := do.MustInvoke[*llm.Client](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
	providerRegistry := do.MustInvoke[*llm.ProviderRegistry](injector)

	// Restore role assignments from DB on startup.
	if assignments, err := providerRegistry.Assignments(context.Background()); err == nil {
		for _, a := range assignments {
			spec, err := providerRegistry.BuildRoleSpec(context.Background(), a.ProviderName, a.ModelID)
			if err != nil {
				logger.Warn("ロール復元: RoleSpec 構築失敗", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID, "error", err)
				continue
			}
			llmClient.SwapRoleSpec(a.Role, *spec)
			ag.OnRoleSpecChanged(a.Role, *spec)
			logger.Info("LLMロールを復元", "role", a.Role, "provider", a.ProviderName, "model", a.ModelID)
		}
	}

	// LLM management (control API).
	mux.Handle("GET /internal/llm", controlOgen)
	mux.Handle("GET /internal/llm/providers", controlOgen)
	mux.Handle("GET /internal/llm/models", controlOgen)
	mux.Handle("POST /internal/llm/models", controlOgen)
	mux.Handle("POST /internal/llm/models/refresh", controlOgen)
	mux.Handle("GET /internal/llm/roles", controlOgen)
	mux.Handle("PUT /internal/llm/roles/{role}", controlOgen)

	// Playground: chat with LLM using current context snapshot (read-only).
	// Tool registry: restore disabled set on startup, then delegate to control API.
	registry := do.MustInvoke[*tool.Registry](injector)
	if names, err := tool.LoadDisabled(context.Background(), llmDB); err != nil {
		logger.Warn("disabled tools の復元に失敗", "error", err)
	} else if len(names) > 0 {
		registry.SetDisabled(names)
		logger.Info("restored disabled tools", "count", len(names))
	}

	mux.Handle("GET /internal/tools", controlOgen)
	mux.Handle("PUT /internal/tools/{name}/enabled", controlOgen)
	mux.Handle("POST /internal/tools/{name}/execute", controlOgen)

	// VOICEVOX speaker management (control API).
	if cfg.Voice.Enabled {
		mux.Handle("GET /internal/voicevox/speakers", controlOgen)
		mux.Handle("GET /internal/voicevox/speaker", controlOgen)
		mux.Handle("PUT /internal/voicevox/speaker", controlOgen)
	}

	// Physical device (ESP32 + Web widget). Hub と vision は DI で構築済み。
	// WS/binary/SSE 系は raw handler、JSON 系は control API に委譲する。
	hub := do.MustInvoke[*device.Hub](injector)
	visionFeature := do.MustInvoke[*vision.Feature](injector)
	mux.HandleFunc("GET /ws/device", hub.Handler())
	mux.HandleFunc("GET /ws/web", hub.WebHandler())
	mux.HandleFunc("GET /internal/device/frame", visionFeature.Frames().FrameHandler())
	mux.HandleFunc("GET /internal/device/detections", visionFeature.Frames().DetectionStreamHandler())
	mux.Handle("GET /internal/device/vision", controlOgen)
	mux.Handle("PUT /internal/device/vision", controlOgen)
	mux.Handle("POST /internal/device/servo", controlOgen)
	mux.Handle("PUT /internal/device/volume", controlOgen)
	mux.Handle("GET /internal/device/tracker", controlOgen)
	mux.Handle("PUT /internal/device/tracker", controlOgen)

	ag.SetSession(agent.SourceKeyDevice, agent.NewDeviceSession(
		ag.AgentContextFor(agent.SourceKeyDevice), hub, logger,
	))
	ag.SetSession(agent.SourceKeyWeb, agent.NewWebSession(
		ag.AgentContextFor(agent.SourceKeyWeb), hub, logger,
	))
	gw.Register(device.NewSource(hub))

	// Start periodic capture loop (333ms = ~3fps).
	captureCtx, captureCancel := context.WithCancel(context.Background())
	defer captureCancel()
	hub.StartCaptureLoop(captureCtx, 333)
	logger.Info("デバイス接続口を開いた")

	// Overland location tracking endpoint.
	locStore := do.MustInvoke[*location.Store](injector)
	if locStore != nil {
		// overland は token ベースの認証付き raw handler のまま。
		locHandler := location.NewHandler(locStore, cfg.Location.Token, logger)
		mux.Handle("POST /internal/overland", locHandler)
		mux.Handle("POST /internal/reload-location-settings", controlOgen)
		logger.Info("overland location endpoint enabled")
	}

	logger.Info("internal server starting", "addr", cfg.Observe.InternalAddr)
	if err := http.ListenAndServe(cfg.Observe.InternalAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
