package di

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/embedder/gemini"
	"github.com/haryoiro/suzuha/internal/adapter/embedder/textonly"
	llmgemini "github.com/haryoiro/suzuha/internal/adapter/llm/gemini"
	llmopenai "github.com/haryoiro/suzuha/internal/adapter/llm/openai"
	llmzhipu "github.com/haryoiro/suzuha/internal/adapter/llm/zhipu"
	"github.com/haryoiro/suzuha/internal/adapter/store/conversation"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	userStore "github.com/haryoiro/suzuha/internal/adapter/store/user"
	"github.com/haryoiro/suzuha/internal/adapter/stt"
	"github.com/haryoiro/suzuha/internal/adapter/transcript"
	"github.com/haryoiro/suzuha/internal/adapter/tts"
	"github.com/haryoiro/suzuha/internal/adapter/twitter"
	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/api/control"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/behavior/action"
	"github.com/haryoiro/suzuha/internal/behavior/builtin"
	"github.com/haryoiro/suzuha/internal/behavior/research"
	"github.com/haryoiro/suzuha/internal/behavior/video"
	convcap "github.com/haryoiro/suzuha/internal/capability/conversation"
	"github.com/haryoiro/suzuha/internal/capability/conversation/boredom"
	"github.com/haryoiro/suzuha/internal/capability/llm"
	"github.com/haryoiro/suzuha/internal/capability/mcp"
	memcap "github.com/haryoiro/suzuha/internal/capability/memory"
	capmemAcq "github.com/haryoiro/suzuha/internal/capability/memory/acquire"
	capmemCon "github.com/haryoiro/suzuha/internal/capability/memory/consolidate"
	"github.com/haryoiro/suzuha/internal/capability/memory/forget"
	"github.com/haryoiro/suzuha/internal/capability/memory/summarize"
	"github.com/haryoiro/suzuha/internal/capability/vision"
	"github.com/haryoiro/suzuha/internal/channel/cli"
	"github.com/haryoiro/suzuha/internal/channel/device"
	"github.com/haryoiro/suzuha/internal/channel/discord"
	"github.com/haryoiro/suzuha/internal/channel/web"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/lib/crypto"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/port/chat"
	embedding "github.com/haryoiro/suzuha/internal/port/embedder"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
	"github.com/haryoiro/suzuha/internal/port/user"
	"github.com/haryoiro/suzuha/internal/runtime/agent"
	"github.com/haryoiro/suzuha/internal/runtime/agent/prompt"
	"github.com/haryoiro/suzuha/internal/runtime/event"
	"github.com/haryoiro/suzuha/internal/runtime/gateway"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler/notification"
	toolreg "github.com/haryoiro/suzuha/internal/runtime/toolregistry"
	"github.com/samber/do/v2"
)

// Packages returns all DI package functions for the agent process.
// cmd は samber/do.New に渡すだけで全配線が完了する。
func Packages(cfgPath string) []func(do.Injector) {
	return []func(do.Injector){
		agentPackages(cfgPath),
		config.Package,
		observe.Package,
		event.Package,
		toolreg.Package,
		memory.Package,
		llm.Package,
		mcp.Package,
		mementoPackage,
		userStore.Package,
		convcap.Package,
	}
}

// agentPackages returns a DI package function that registers all
// cross-cutting providers that cannot live in internal packages
// due to import cycles.
func agentPackages(cfgPath string) func(do.Injector) {
	return func(i do.Injector) {
		// Named value: config file path.
		do.ProvideNamedValue(i, "config-path", cfgPath)

		// Embedder bridges the embedding interface to the configured provider.
		do.ProvideNamed(i, "embedder", func(i do.Injector) (embedding.Embedder, error) {
			cfg := do.MustInvoke[*config.Config](i)
			switch cfg.Embedding.Provider {
			case "gemini":
				return gemini.NewGeminiEmbedder(cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Dims)
			default:
				// OpenAI etc: llm.Client satisfies embedding.TextEmbedClient.
				llmClient := do.MustInvoke[*llm.Client](i)
				return textonly.NewTextOnlyEmbedder(llmClient, cfg.Embedding.Dims), nil
			}
		})

		// Shared DB extracted from memory store.
		do.ProvideNamed(i, "shared-db", func(i do.Injector) (*sql.DB, error) {
			return do.MustInvoke[memory.Backend](i).DB(), nil
		})

		// Provider Registry (3-layer model: providers / models / roles).
		do.Provide(i, func(i do.Injector) (*llm.ProviderRegistry, error) {
			cfg := do.MustInvoke[*config.Config](i)
			db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
			logger := do.MustInvoke[*slog.Logger](i)

			if cfg.EncryptionKey == "" {
				return nil, fmt.Errorf("SUZUHA_ENCRYPTION_KEY が設定されていません")
			}
			cipher, err := crypto.NewAESGCMCipher(cfg.EncryptionKey)
			if err != nil {
				return nil, fmt.Errorf("暗号化の初期化に失敗: %w", err)
			}
			metas := map[string]portllm.ProviderMeta{
				"openai": llmopenai.NewMeta(),
				"zhipu":  llmzhipu.NewMeta(),
				"gemini": llmgemini.NewMeta(),
				"qwen":   llmopenai.NewMeta(), // OpenAI互換
			}
			reg := llm.NewProviderRegistry(db, cipher, metas, logger)

			// 旧 llm_presets からの自動マイグレーション
			if err := reg.MigrateFromPresets(context.Background()); err != nil {
				logger.Warn("旧プリセットからの移行に失敗", "error", err)
			}

			// config.yaml のプロバイダ定義をシード
			if err := reg.SeedProviders(context.Background(), cfg.LLM.Providers); err != nil {
				logger.Warn("プロバイダのシードに失敗", "error", err)
			}

			// 静的モデルカタログを最新の定義で同期
			if err := reg.SeedStaticModels(context.Background()); err != nil {
				logger.Warn("静的モデルカタログのシードに失敗", "error", err)
			}

			return reg, nil
		})

		// Discord chat instance (nil when Discord is not configured).
		do.Provide(i, func(i do.Injector) (*discord.Chat, error) {
			cfg := do.MustInvoke[*config.Config](i)
			if cfg.Discord.Token == "" {
				return nil, nil
			}
			bus := do.MustInvoke[*event.Bus](i)
			logger := do.MustInvoke[*slog.Logger](i)
			return discord.New(cfg.Discord.Token, cfg.Discord.BotID, bus, logger), nil
		})

		// Chat interface: Discord if token is set, otherwise CLI.
		do.Provide(i, func(i do.Injector) (chat.Interface, error) {
			cfg := do.MustInvoke[*config.Config](i)
			logger := do.MustInvoke[*slog.Logger](i)
			if cfg.Discord.Token != "" {
				dc := do.MustInvoke[*discord.Chat](i)
				logger.Info("チャットモード: discord")
				return dc, nil
			}
			bus := do.MustInvoke[*event.Bus](i)
			logger.Info("チャットモード: cli")
			return cli.New(os.Stdin, os.Stdout, bus), nil
		})

		// Agent.
		do.Provide(i, func(i do.Injector) (*agent.Agent, error) {
			cfg := do.MustInvoke[*config.Config](i)
			chatIface := do.MustInvoke[chat.Interface](i)
			channelSettings := do.MustInvoke[*convcap.SettingsStore](i)
			logger := do.MustInvoke[*slog.Logger](i)

			regs := []agent.SourceRegistration{
				{
					Key: agent.SourceKeyDiscord,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return discord.NewSession(agentCtx, chatIface, nil, channelSettings, agent.DefaultDrainWindow, logger)
					},
					PersistKey: "discord",
				},
				{
					Key: agent.SourceKeyDevice,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return device.NewSession(agentCtx, nil, logger)
					},
					PersistKey: "device",
				},
				{
					Key: agent.SourceKeyWeb,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return web.NewSession(agentCtx, nil, logger)
					},
					PersistKey: "web",
				},
			}

			db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
			diaryReader := &diaryReaderAdapter{store: summarize.NewStore(db)}
			memBackend := do.MustInvoke[memory.Backend](i)
			userStore := do.MustInvoke[user.Store](i)

			providers := []prompt.Provider{
				&prompt.DiaryProvider{Reader: diaryReader, Logger: logger},
				&prompt.MemoryProvider{Memory: memBackend, Logger: logger},
				&prompt.ProfileProvider{Users: userStore, Memory: memBackend, BotID: cfg.Discord.BotID, Logger: logger},
				&prompt.ChannelProvider{},
				prompt.SelfPromptProvider{},
			}

			return agent.New(
				agent.Config{
					SystemPrompt:     cfg.Agent.SystemPrompt,
					BotID:            cfg.Discord.BotID,
					ContextWindowPct: cfg.Agent.ContextWindowPct,
					MaxContextTokens: cfg.LLM.MaxTokens,
				},
				regs,
				do.MustInvoke[*llm.Client](i).AsPortClient(),
				do.MustInvoke[*toolreg.Registry](i),
				memBackend,
				userStore,
				do.MustInvoke[*event.Bus](i),
				do.MustInvoke[*capmemAcq.Acquirer](i),
				conversation.NewDBStore(db),
				providers,
				channelSettings,
				logger,
			), nil
		})

		// Langfuse TracerProvider (nil when disabled).
		do.Provide(i, func(i do.Injector) (*langfuse.TracerProvider, error) {
			cfg := do.MustInvoke[*config.Config](i)
			if !cfg.Langfuse.Enabled {
				return nil, nil
			}
			logger := do.MustInvoke[*slog.Logger](i)
			tp, err := langfuse.NewTracerProvider(context.Background(), langfuse.Config{
				Endpoint:  cfg.Langfuse.Endpoint,
				PublicKey: cfg.Langfuse.PublicKey,
				SecretKey: cfg.Langfuse.SecretKey,
			})
			if err != nil {
				logger.Warn("Langfuseの初期化に失敗", "error", err)
				return nil, nil
			}
			logger.Info("Langfuseトレーシングを有効化", "endpoint", cfg.Langfuse.Endpoint)
			return tp, nil
		})

		// Tasks + tool registration.
		// 旧 scheduler.Feature bundle を廃止し、tool は registry に直接登録、
		// task は []scheduler.CronTask として返す。Setup 相当の初期化
		// (action.Store.Setup や mcp apps 再接続等) は適宜呼び出す。
		do.Provide(i, func(i do.Injector) ([]scheduler.CronTask, error) {
			store := do.MustInvoke[memory.Backend](i)
			registry := do.MustInvoke[*toolreg.Registry](i)
			logger := do.MustInvoke[*slog.Logger](i)
			mcpMgr := do.MustInvoke[*mcp.Manager](i)
			ag := do.MustInvoke[*agent.Agent](i)
			userStore := do.MustInvoke[user.Store](i)
			llmClient := do.MustInvoke[*llm.Client](i)

			ctx := context.Background()
			db := store.DB()

			// builtin tools.
			registry.Register(research.NewFetch())
			registry.Register(builtin.NewPythonExec())
			registry.Register(builtin.NewUpdateUserProfile(userStore, func(userID, newName string) {
				ag.AgentContext().UpdateUserName(userID, newName)
			}))
			registry.Register(memcap.NewMemoCreate(store))
			registry.Register(memcap.NewMemoSearch(store))
			registry.Register(memcap.NewMemoUpdate(store))

			// action (scheduled_actions) — setup + tools + task.
			actionStore := action.NewStore(db)
			if err := actionStore.Setup(ctx); err != nil {
				logger.Error("action: Setup に失敗", "error", err)
			}
			registry.Register(action.NewCreateTool(actionStore))
			registry.Register(action.NewListTool(actionStore))
			registry.Register(action.NewCancelTool(actionStore))

			// mcp apps — store setup + reconnect + tools.
			mcpAppStore, err := mcp.BootstrapStore(ctx, db)
			if err != nil {
				logger.Error("mcpapps: Setup に失敗", "error", err)
			} else {
				mcp.ReconnectEnabled(ctx, mcpMgr, mcpAppStore, logger)
				for _, t := range mcp.NewTools(mcpMgr, mcpAppStore, logger) {
					registry.Register(t)
				}
			}

			// research (searxng は compose network 内に常在)。
			searxURL := "http://searxng:8080"
			registry.Register(research.NewResearchTool(searxURL, 5))

			// video (transcript fetcher + YOLO frame extractor)。
			ytFetcher := transcript.NewYouTubeFetcher()
			videoFetcher := transcript.NewChain(logger,
				ytFetcher,
				transcript.NewYtDlpFetcher(),
			)
			videoExtractor := transcript.NewYtDlpFrameExtractor()
			ag.SetVideoMeta(&videoMetaAdapter{inner: ytFetcher}, transcript.ExtractVideoURLs)
			ag.SetTweetFetcher(&tweetFetcherAdapter{inner: twitter.NewFxTwitterFetcher()}, twitter.ExtractTwitterURLs)

			registry.Register(video.NewWatchTool(videoFetcher, logger))
			if videoExtractor != nil && llmClient != nil {
				registry.Register(video.NewLookTool(videoExtractor, llmClient.AsPortClient(), logger))
			}

			// vision (デバイスブロックで作成されていれば)。
			if vf, err := do.Invoke[*vision.Service](i); err == nil {
				for _, t := range vf.Tools() {
					registry.Register(t)
				}
			}

			// Langfuse トレーシングの配線 (有効なら)。
			lfTP := do.MustInvoke[*langfuse.TracerProvider](i)
			if lfTP != nil {
				tracer := lfTP.Tracer("suzuha-agent")
				llmClient.SetTracer(tracer)
				ag.SetTracer(tracer)
				ag.AddHook(newLangfuseAdapter(langfuse.NewHook(tracer)))
			}

			// memory attachment 読み込み用の media store を配線。
			ag.SetMediaStore(do.MustInvoke[memory.MediaStore](i))

			// tool 登録完了のマーカーとして空 slice を返す。task 本体は
			// provideScheduler 内で notifier 等を揃えてから構築する。
			return []scheduler.CronTask{}, nil
		})

		// Schedule store (used by admin server).
		do.Provide(i, func(i do.Injector) (*action.Store, error) {
			store := do.MustInvoke[memory.Backend](i)
			return action.NewStore(store.DB()), nil
		})

		// Media store for binary attachments (images, audio).
		do.Provide(i, func(i do.Injector) (memory.MediaStore, error) {
			ms, err := memory.NewLocalMediaStore("/data/media")
			if err != nil {
				return nil, err
			}
			// Wire media store into memory store for embedding with attachments.
			do.MustInvoke[memory.Backend](i).SetMediaStore(ms)
			return ms, nil
		})

		// Admin server (uses a plain logger without ring buffer to avoid
		// flooding the agent log stream with HTTP access logs).
		do.Provide(i, func(i do.Injector) (*admin.Server, error) {
			cfg := do.MustInvoke[*config.Config](i)
			store := do.MustInvoke[memory.Backend](i)
			userStore := do.MustInvoke[user.AdminStore](i)
			schedStore := do.MustInvoke[*action.Store](i)
			mediaStore := do.MustInvoke[memory.MediaStore](i)
			adminLogger := observe.NewLogger(cfg.Observe.LogLevel)

			db := store.DB()
			// 型が domain 経由で共有されるようになったので、feature の Store を
			// そのまま admin.ActionStore / admin.DiaryStore として渡せる。
			var ds admin.DiaryStore = summarize.NewStore(db)
			var as admin.ActionStore = schedStore

			return admin.NewServer(cfg.Admin, store, userStore, as, ds, mediaStore, adminLogger)
		})

		// Scheduler (nil when disabled in config).
		do.Provide(i, provideScheduler)

		// Gateway は各 Source のライフサイクルを管理する。
		do.Provide(i, func(i do.Injector) (*gateway.Gateway, error) {
			return gateway.New(do.MustInvoke[*slog.Logger](i)), nil
		})

		// Device Hub (ESP32 WebSocket + Web widget) and Vision service.
		do.Provide(i, provideDeviceHub)
		do.Provide(i, provideVisionService)

		// Control (internal) API — sub-handler ごとに DI 登録し、
		// control.NewHandler が合成する。
		do.Provide(i, control.NewRuntimeHandler)
		do.Provide(i, control.NewAgentHandler)
		do.Provide(i, control.NewSchedulerHandler)
		do.Provide(i, control.NewVoicevoxHandler)
		do.Provide(i, control.NewToolsHandler)
		do.Provide(i, control.NewLLMHandler)
		do.Provide(i, control.NewDeviceHandler)
		do.Provide(i, control.NewRawHandler)
		do.Provide(i, func(i do.Injector) (gen.Handler, error) {
			return control.NewHandler(i), nil
		})
	}
}

func provideScheduler(i do.Injector) (*scheduler.Scheduler, error) {
	cfg := do.MustInvoke[*config.Config](i)
	if !cfg.Consolidator.Scheduler.Enabled {
		return nil, nil
	}

	llmClient := do.MustInvoke[*llm.Client](i)
	store := do.MustInvoke[memory.Backend](i)
	ring := do.MustInvoke[*observe.RingBuffer](i)
	logger := observe.NewLoggerWithRing(do.MustInvoke[*config.Config](i).Observe.LogLevel, ring)
	chatIface := do.MustInvoke[chat.Interface](i)
	userStore := do.MustInvoke[user.Store](i)
	_ = do.MustInvoke[[]scheduler.CronTask](i) // tool 登録フェーズを発火させる
	bus := do.MustInvoke[*event.Bus](i)
	db := store.DB()

	// Build notifier with middleware chain.
	var notifier notification.Notifier = notification.NewChatNotifier(chatIface, logger)

	schedulerLoc := time.UTC
	if tz := cfg.Timezone; tz != "" {
		if parsed, tzErr := time.LoadLocation(tz); tzErr == nil {
			schedulerLoc = parsed
		} else {
			logger.Warn("scheduler: 無効なタイムゾーンです。UTCを使用します", "timezone", tz, "error", tzErr)
		}
	}
	jtime.Init(schedulerLoc)
	logger.Info("timezone", "location", schedulerLoc.String())

	if cfg.Consolidator.Scheduler.QuietHours.Enabled {
		notifier = notification.WithQuietHours(notification.QuietHoursConfig{
			Start:    cfg.Consolidator.Scheduler.QuietHours.Start,
			End:      cfg.Consolidator.Scheduler.QuietHours.End,
			Location: schedulerLoc,
		}, logger)(notifier)
		logger.Info("scheduler: 静寂時間を有効化しました",
			"start", cfg.Consolidator.Scheduler.QuietHours.Start,
			"end", cfg.Consolidator.Scheduler.QuietHours.End,
			"timezone", schedulerLoc.String(),
		)
	}

	channelSettings := do.MustInvoke[*convcap.SettingsStore](i)
	notifier = notification.WithChannelSettings(channelSettings, logger)(notifier)

	// 各 task を constructor 注入で構築して registry に登録する。
	// CronContext (神オブジェクト) を介さず、各 task が必要な依存だけを受ける。
	activityStore := convcap.NewActivityStore(db)
	portLLM := llmClient.AsPortClient()
	systemPrompt := cfg.Agent.SystemPrompt

	taskRegistry := scheduler.NewRegistry()
	taskRegistry.Register(action.NewTask(db, portLLM, notifier, systemPrompt, logger))
	taskRegistry.Register(research.NewTask(db, logger))
	taskRegistry.Register(boredom.NewTask(db, store, userStore, activityStore, bus, logger))
	taskRegistry.Register(summarize.NewHourlyTask(db, portLLM, store, systemPrompt, logger))
	taskRegistry.Register(summarize.NewDailyTask(db, portLLM, systemPrompt, logger))
	taskRegistry.Register(forget.NewTask(do.MustInvoke[*capmemCon.Consolidator](i), db, logger))

	sched := scheduler.New(taskRegistry, schedulerLoc, logger)
	if err := sched.Setup(context.Background()); err != nil {
		return nil, fmt.Errorf("scheduler セットアップに失敗: %w", err)
	}

	// Convert config jobs to scheduler JobDefs.
	jobDefs := make([]scheduler.JobDef, len(cfg.Consolidator.Scheduler.Jobs))
	for idx, j := range cfg.Consolidator.Scheduler.Jobs {
		jobDefs[idx] = scheduler.JobDef{
			Name:   j.Name,
			Task:   j.Task,
			Cron:   j.Cron,
			Config: j.Config,
		}
	}
	if err := sched.LoadJobs(jobDefs); err != nil {
		return nil, fmt.Errorf("scheduler ジョブの読み込みに失敗: %w", err)
	}

	return sched, nil
}

// provideDeviceHub は ESP32/Web ウィジェット用の device.Hub を構築する。
// voice/TTS/STT の設定、DB の owner/home 情報を裏で解決する。
func provideDeviceHub(i do.Injector) (*device.Hub, error) {
	cfg := do.MustInvoke[*config.Config](i)
	bus := do.MustInvoke[*event.Bus](i)
	logger := do.MustInvoke[*slog.Logger](i)
	db := do.MustInvokeNamed[*sql.DB](i, "shared-db")

	var ttsClient tts.TTS
	if cfg.Voice.Enabled && len(cfg.Voice.TTS) > 0 {
		deviceTTSConfigs := make([]tts.TTSProviderConfig, len(cfg.Voice.TTS))
		for idx, p := range cfg.Voice.TTS {
			deviceTTSConfigs[idx] = tts.TTSProviderConfig{
				Provider:  p.Provider,
				URL:       p.URL,
				SpeakerID: p.SpeakerID,
				Model:     p.Model,
				Style:     p.Style,
			}
		}
		c, err := tts.NewTTSChain(deviceTTSConfigs, logger)
		if err != nil {
			logger.Error("device hub: TTS クライアント初期化失敗", "error", err)
		} else {
			ttsClient = c
		}
	}

	var sttClient stt.STT
	if cfg.Voice.Enabled && len(cfg.Voice.STT) > 0 {
		c, err := stt.NewSTT(stt.STTProviderConfig{
			Provider: cfg.Voice.STT[0].Provider,
			APIKey:   cfg.Voice.STT[0].APIKey,
			Model:    cfg.Voice.STT[0].Model,
			URL:      cfg.Voice.STT[0].URL,
		})
		if err != nil {
			logger.Error("device hub: STT クライアント初期化失敗", "error", err)
		} else {
			sttClient = c
		}
	}

	var ownerID, ownerName string
	if err := db.QueryRow(`SELECT id, display_name FROM users WHERE role = 'owner' LIMIT 1`).Scan(&ownerID, &ownerName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Error("device hub: オーナー情報取得失敗", "error", err)
	}
	if ownerID == "" {
		ownerID = "owner"
		ownerName = "オーナー"
	}

	return device.NewHub(bus, ttsClient, sttClient, ownerID, ownerName, logger), nil
}

// provideVisionService は device.Hub を使う vision.Service を構築する。
// hub.SetImageHandler で vision pipeline に画像を流し込む配線も行う。
func provideVisionService(i do.Injector) (*vision.Service, error) {
	cfg := do.MustInvoke[*config.Config](i)
	bus := do.MustInvoke[*event.Bus](i)
	logger := do.MustInvoke[*slog.Logger](i)
	db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
	llmClient := do.MustInvoke[*llm.Client](i)
	hub := do.MustInvoke[*device.Hub](i)

	yoloURL := os.Getenv("YOLO_URL")
	if yoloURL == "" {
		yoloURL = "http://yolo:8002"
	}

	var deviceChannel string
	if err := db.QueryRow(`SELECT channel_id FROM channel_settings WHERE home = true LIMIT 1`).Scan(&deviceChannel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Error("vision: ホームチャンネル取得失敗", "error", err)
	}

	_ = cfg // 今は使わないが config 依存を明示しておく
	devAdapter := device.NewDeviceAdapter(hub)
	vf := vision.New(bus, yoloURL, deviceChannel, devAdapter, devAdapter, llmClient, logger)
	hub.SetImageHandler(vf.Pipeline())
	return vf, nil
}

// mementoPackage registers memento sub-package providers into the DI injector.
func mementoPackage(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*capmemAcq.Acquirer, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[memory.Backend](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return capmemAcq.NewAcquirer(llmClient.For("background"), store, capmemAcq.DefaultConfig(), logger), nil
	})

	do.Provide(i, func(i do.Injector) (*capmemCon.Consolidator, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[memory.Backend](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return capmemCon.NewConsolidator(llmClient.For("background"), store, store, logger), nil
	})
}
