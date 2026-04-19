package di

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/external/transcript"
	"github.com/haryoiro/suzuha/external/twitter"
	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/api/control"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/conversation"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/adapter/cli"
	"github.com/haryoiro/suzuha/internal/adapter/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/feature/action"
	"github.com/haryoiro/suzuha/internal/feature/diary"
	"github.com/haryoiro/suzuha/internal/feature/forget"
	"github.com/haryoiro/suzuha/internal/feature/research"
	"github.com/haryoiro/suzuha/internal/feature/topics"
	"github.com/haryoiro/suzuha/internal/feature/video"
	"github.com/haryoiro/suzuha/internal/feature/vision"
	"github.com/haryoiro/suzuha/internal/feature/wander"
	"github.com/haryoiro/suzuha/internal/lib/crypto"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/feature/location"
	"github.com/haryoiro/suzuha/internal/mcp"
	"github.com/haryoiro/suzuha/internal/memento/acquirer"
	"github.com/haryoiro/suzuha/internal/memento/consolidator"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler/notification"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/user"
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
		tool.Package,
		memory.Package,
		llm.Package,
		mcp.Package,
		mementoPackage,
		user.Package,
		channel.Package,
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
				return embedding.NewGeminiEmbedder(cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Dims)
			default:
				// OpenAI etc: llm.Client satisfies embedding.TextEmbedClient.
				llmClient := do.MustInvoke[*llm.Client](i)
				return embedding.NewTextOnlyEmbedder(llmClient, cfg.Embedding.Dims), nil
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
			reg := llm.NewProviderRegistry(db, cipher, logger)

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

		// Location store (nil when location tracking is not configured).
		do.Provide(i, func(i do.Injector) (*location.Store, error) {
			cfg := do.MustInvoke[*config.Config](i)
			if !cfg.Location.Enabled {
				return nil, nil
			}
			db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
			store := location.NewStore(db)
			if err := store.LoadCache(context.Background()); err != nil {
				// Non-fatal: cache will warm on first Overland POST.
			}
			return store, nil
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
			channelSettings := do.MustInvoke[*channel.Store](i)
			logger := do.MustInvoke[*slog.Logger](i)

			regs := []agent.SourceRegistration{
				{
					Key: agent.SourceKeyDiscord,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return agent.NewDiscordSession(agentCtx, chatIface, nil, channelSettings, agent.DefaultDrainWindow, logger)
					},
					PersistKey: "discord",
				},
				{
					Key: agent.SourceKeyDevice,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return agent.NewDeviceSession(agentCtx, nil, logger)
					},
					PersistKey: "device",
				},
				{
					Key: agent.SourceKeyWeb,
					NewSession: func(agentCtx *agent.Context) agent.Session {
						return agent.NewWebSession(agentCtx, nil, logger)
					},
					PersistKey: "web",
				},
			}

			db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
			diaryReader := &diaryReaderAdapter{store: diary.NewStore(db)}

			return agent.New(
				agent.Config{
					SystemPrompt:     cfg.Agent.SystemPrompt,
					BotID:            cfg.Discord.BotID,
					ContextWindowPct: cfg.Agent.ContextWindowPct,
					MaxContextTokens: cfg.LLM.MaxTokens,
					},
				regs,
				do.MustInvoke[*llm.Client](i),
				do.MustInvoke[*tool.Registry](i),
				do.MustInvoke[memory.Backend](i),
				do.MustInvoke[user.Store](i),
				do.MustInvoke[*event.Bus](i),
				do.MustInvoke[*acquirer.Acquirer](i),
				conversation.NewStore(db),
				diaryReader,
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

		// Features: setup + tool/hook registration.
		do.Provide(i, func(i do.Injector) ([]scheduler.Feature, error) {
			cfg := do.MustInvoke[*config.Config](i)
			store := do.MustInvoke[memory.Backend](i)
			registry := do.MustInvoke[*tool.Registry](i)
			logger := do.MustInvoke[*slog.Logger](i)
			mcpMgr := do.MustInvoke[*mcp.Manager](i)
			ag := do.MustInvoke[*agent.Agent](i)
			userStore := do.MustInvoke[user.Store](i)
			llmClient := do.MustInvoke[*llm.Client](i)

			// Register builtin tools.
			registry.Register(builtin.NewFetch())
			registry.Register(builtin.NewPythonExec())
			registry.Register(builtin.NewUpdateUserProfile(userStore, func(userID, newName string) {
				ag.AgentContext().UpdateUserName(userID, newName)
			}))
			registry.Register(builtin.NewMemoCreate(store))
			registry.Register(builtin.NewMemoSearch(store))
			registry.Register(builtin.NewMemoUpdate(store))

			// Web search features (searxng is always on the compose network).
			searxURL := "http://searxng:8080"

			// Video transcript fetcher (YouTube Go library → yt-dlp fallback).
			ytFetcher := transcript.NewYouTubeFetcher()
			videoFetcher := transcript.NewChain(logger,
				ytFetcher,
				transcript.NewYtDlpFetcher(),
			)
			videoExtractor := transcript.NewYtDlpFrameExtractor()
			ag.SetVideoMeta(&videoMetaAdapter{inner: ytFetcher}, transcript.ExtractVideoURLs)
			ag.SetTweetFetcher(&tweetFetcherAdapter{inner: twitter.NewFxTwitterFetcher()}, twitter.ExtractTwitterURLs)

			features := []scheduler.Feature{
				action.New(store.DB()),
				mcp.NewFeature(mcpMgr, logger),
				topics.New(),
				research.New(searxURL, 5),
				wander.New(searxURL, llmClient, store, cfg.Agent.SystemPrompt, 4),
				forget.New(do.MustInvoke[*consolidator.Consolidator](i)),
				diary.New(),
				video.New(videoFetcher, videoExtractor, llmClient, logger),
			}

			// Add vision feature if device block created it.
			if vf, err := do.Invoke[*vision.Feature](i); err == nil {
				features = append(features, vf)
			}

			// Add location feature if enabled.
			locStore := do.MustInvoke[*location.Store](i)
			if locStore != nil {
				features = append(features, location.NewFeature(locStore))
				ag.SetLocationStore(locStore)
			}

			// Wire Langfuse tracing if enabled.
			lfTP := do.MustInvoke[*langfuse.TracerProvider](i)
			if lfTP != nil {
				tracer := lfTP.Tracer("suzuha-agent")
				llmClient.SetTracer(tracer)
				ag.SetTracer(tracer)
				ag.AddHook(newLangfuseAdapter(langfuse.NewHook(tracer)))
			}

			// Set media store for memory attachment loading.
			ag.SetMediaStore(do.MustInvoke[memory.MediaStore](i))
			for _, f := range features {
				if err := f.Setup(context.Background(), store.DB()); err != nil {
					logger.Error("フィーチャーのセットアップに失敗しました", "feature", f.Name(), "error", err)
				}
				for _, t := range f.Tools() {
					registry.Register(t)
				}
				if h, ok := f.(agent.PipelineHook); ok {
					ag.AddHook(h)
					logger.Info("パイプラインフックを登録しました", "feature", f.Name())
				}
			}
			return features, nil
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
			var ds admin.DiaryStore = &diaryStoreAdapter{s: diary.NewStore(db)}
			var ls admin.LocationStore = &locationStoreAdapter{s: location.NewStore(db)}

			return admin.NewServer(cfg.Admin, store, userStore, &actionStoreAdapter{s: schedStore}, ds, ls, mediaStore, adminLogger)
		})

		// Scheduler (nil when disabled in config).
		do.Provide(i, provideScheduler)

		// Control (internal) API handler — mount on internal mux.
		do.Provide(i, func(i do.Injector) (*control.Handler, error) {
			cfg := do.MustInvoke[*config.Config](i)
			ag := do.MustInvoke[*agent.Agent](i)
			channelStore := do.MustInvoke[*channel.Store](i)
			userStore := do.MustInvoke[user.Store](i)
			cfgPath := do.MustInvokeNamed[string](i, "config-path")
			return control.NewHandler(ag, channelStore, userStore, cfg.Agent.PromptDir, filepath.Dir(cfgPath)), nil
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
	features := do.MustInvoke[[]scheduler.Feature](i)

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

	notifier = notification.WithChannelSettings(store.DB(), logger)(notifier)

	// Build task registry from features.
	taskRegistry := scheduler.NewRegistry()
	for _, f := range features {
		for _, t := range f.Tasks() {
			taskRegistry.Register(t)
		}
	}

	// Build CronContext.
	bus := do.MustInvoke[*event.Bus](i)
	activityStore := channel.NewActivityStore(store.DB())
	mediaStore := do.MustInvoke[memory.MediaStore](i)
	cc := &scheduler.CronContext{
		LLM:             llmClient,
		Memory:          store,
		Notifier:        notifier,
		DB:              store.DB(),
		Logger:          logger,
		Users:           userStore,
		ChannelActivity: activityStore,
		MemoryAdmin:     store,
		MediaStore:      mediaStore,
		Bus:             bus,
		Timezone:        schedulerLoc,
		SystemPrompt:    cfg.Agent.SystemPrompt,
	}

	sched := scheduler.New(taskRegistry, cc, logger)
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

// mementoPackage registers memento sub-package providers into the DI injector.
func mementoPackage(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*acquirer.Acquirer, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[memory.Backend](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return acquirer.NewAcquirer(llmClient.For("background"), store, acquirer.DefaultConfig(), logger), nil
	})

	do.Provide(i, func(i do.Injector) (*consolidator.Consolidator, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[memory.Backend](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return consolidator.NewConsolidator(llmClient.For("background"), store, store, logger), nil
	})
}
