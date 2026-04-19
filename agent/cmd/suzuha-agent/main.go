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
	"github.com/haryoiro/suzuha/internal/adapter/stt"
	"github.com/haryoiro/suzuha/internal/adapter/tts"
	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/api/control"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/agent"
	convcap "github.com/haryoiro/suzuha/internal/capability/conversation"
	"github.com/haryoiro/suzuha/internal/port/chat"
	"github.com/haryoiro/suzuha/internal/channel/cli"
	"github.com/haryoiro/suzuha/internal/channel/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/channel/device"
	"github.com/haryoiro/suzuha/internal/channel/web"
	"github.com/haryoiro/suzuha/internal/di"
	"github.com/haryoiro/suzuha/internal/runtime/gateway"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/capability/mcp"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
	toolreg "github.com/haryoiro/suzuha/internal/runtime/toolregistry"
	"github.com/haryoiro/suzuha/internal/port/user"
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
	_ = do.MustInvoke[[]scheduler.CronTask](injector) // triggers tool registration + task list build

	sched := do.MustInvoke[*scheduler.Scheduler](injector)
	if sched != nil {
		sched.Start()
		logger.Info("scheduler started", "jobs", len(cfg.Consolidator.Scheduler.Jobs))
		defer func() {
			sched.Stop()
			logger.Info("scheduler stopped")
		}()
	}

	// Gateway は DI 登録済み。startInternalHTTP で Device source を登録する。
	gw := do.MustInvoke[*gateway.Gateway](injector)

	// Register Discord OnReady callback.
	dc := do.MustInvoke[*discord.Chat](injector)
	if dc != nil {
		registerDiscordOnReady(injector, dc)
		gw.Register(dc)
	} else {
		gw.Register(chatIface.(gateway.Source))
		ag.SetSession(agent.SourceKeyCLI, cli.NewSession(
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
	channelStore := do.MustInvoke[*convcap.SettingsStore](injector)
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
	registry := do.MustInvoke[*toolreg.Registry](injector)
	userStore := do.MustInvoke[user.Store](injector)
	botReg := do.MustInvoke[user.BotRegistrar](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	cfg := do.MustInvoke[*config.Config](injector)

	dc.OnReady(func(s *discordgo.Session) {
		// Register Discord-specific tools.
		for _, t := range discord.NewDiscordTools(s) {
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
						if ds, ok := ag.GetSession(agent.SourceKeyDiscord).(*discord.Session); ok {
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
	ag := do.MustInvoke[*agent.Agent](injector)

	controlHandler := do.MustInvoke[gen.Handler](injector)
	controlOgen, err := control.NewOgenHandler(controlHandler)
	if err != nil {
		logger.Error("control API の初期化に失敗", "error", err)
		return
	}

	// DB-backed state の起動時復元 (handler が使う前に済ませる)。
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
	llmClient := do.MustInvoke[*llm.Client](injector)
	providerRegistry := do.MustInvoke[*llm.ProviderRegistry](injector)
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
	registry := do.MustInvoke[*toolreg.Registry](injector)
	if names, err := toolreg.LoadDisabled(context.Background(), llmDB); err != nil {
		logger.Warn("disabled tools の復元に失敗", "error", err)
	} else if len(names) > 0 {
		registry.SetDisabled(names)
		logger.Info("restored disabled tools", "count", len(names))
	}

	// ogen-backed control API が /internal/* を全部処理する
	// (SSE/binary/JSON/bearer-auth すべて含む)。WebSocket のみ ogen スコープ外。
	hub := do.MustInvoke[*device.Hub](injector)
	mux := http.NewServeMux()
	mux.Handle("/internal/", controlOgen)
	mux.HandleFunc("GET /ws/device", hub.Handler())
	mux.HandleFunc("GET /ws/web", hub.WebHandler())

	// Physical device session / gateway 配線。
	ag.SetSession(agent.SourceKeyDevice, device.NewSession(
		ag.AgentContextFor(agent.SourceKeyDevice), hub, logger,
	))
	ag.SetSession(agent.SourceKeyWeb, web.NewSession(
		ag.AgentContextFor(agent.SourceKeyWeb), hub, logger,
	))
	gw.Register(device.NewSource(hub))

	// Start periodic capture loop (333ms = ~3fps).
	captureCtx, captureCancel := context.WithCancel(context.Background())
	defer captureCancel()
	hub.StartCaptureLoop(captureCtx, 333)
	logger.Info("デバイス接続口を開いた")


	logger.Info("internal server starting", "addr", cfg.Observe.InternalAddr)
	if err := http.ListenAndServe(cfg.Observe.InternalAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
