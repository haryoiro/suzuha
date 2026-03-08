package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/affinity"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/cli"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/dyntools"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/explore"
	"github.com/haryoiro/suzuha/internal/forget"
	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/location"
	"github.com/haryoiro/suzuha/internal/mcp"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/notification"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/preferences"
	"github.com/haryoiro/suzuha/internal/rss"
	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/topics"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/samber/do/v2"
)

// allPackages returns all DI package functions for the agent process.
func allPackages(cfgPath string) []func(do.Injector) {
	return []func(do.Injector){
		agentPackages(cfgPath),
		config.Package,
		observe.Package,
		event.Package,
		tool.Package,
		memory.Package,
		llm.Package,
		mcp.Package,
		consolidator.Package,
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

		// EmbedFunc bridges memory.EmbedFunc → llm.Client.Embed.
		// Uses closure so memory package doesn't need to import llm.
		do.ProvideNamed(i, "embed-func", func(i do.Injector) (memory.EmbedFunc, error) {
			return func(ctx context.Context, text string) ([]float32, error) {
				return do.MustInvoke[*llm.Client](i).Embed(ctx, text)
			}, nil
		})

		// Shared DB extracted from memory store.
		do.ProvideNamed(i, "shared-db", func(i do.Injector) (*sql.DB, error) {
			return do.MustInvoke[*memory.SQLiteStore](i).DB(), nil
		})

		// Metrics (SQLite-backed, persists across restarts).
		do.Provide(i, func(i do.Injector) (*observe.Metrics, error) {
			store := do.MustInvoke[*memory.SQLiteStore](i)
			m := observe.NewMetrics(store.DB())
			store.SetOnSave(m.MemoryWritesTotal.Inc)
			return m, nil
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
			return agent.New(
				agent.Config{
					SystemPrompt:     cfg.Agent.SystemPrompt,
					BotID:            cfg.Discord.BotID,
					ContextWindowPct: cfg.Agent.ContextWindowPct,
					MaxContextTokens: cfg.LLM.MaxTokens,
				},
				do.MustInvoke[*llm.Client](i),
				do.MustInvoke[*tool.Registry](i),
				do.MustInvoke[*memory.SQLiteStore](i),
				do.MustInvoke[*user.SQLiteStore](i),
				do.MustInvoke[*event.Bus](i),
				do.MustInvoke[chat.Interface](i),
				do.MustInvoke[*consolidator.Server](i),
				do.MustInvokeNamed[*sql.DB](i, "shared-db"),
				do.MustInvoke[*channel.Store](i),
				do.MustInvoke[*slog.Logger](i),
				do.MustInvoke[*observe.Metrics](i),
			), nil
		})

		// Features: setup + tool/hook registration.
		do.Provide(i, func(i do.Injector) ([]scheduler.Feature, error) {
			cfg := do.MustInvoke[*config.Config](i)
			store := do.MustInvoke[*memory.SQLiteStore](i)
			registry := do.MustInvoke[*tool.Registry](i)
			logger := do.MustInvoke[*slog.Logger](i)
			mcpMgr := do.MustInvoke[*mcp.Manager](i)
			ag := do.MustInvoke[*agent.Agent](i)
			userStore := do.MustInvoke[*user.SQLiteStore](i)
			llmClient := do.MustInvoke[*llm.Client](i)

			// Register builtin tools.
			registry.Register(builtin.NewFetch())
			registry.Register(builtin.NewUpdateUserProfile(userStore, func(userID, newName string) {
				ag.AgentContext().UpdateUserName(userID, newName)
			}))

			// Extract explore config from scheduler jobs.
			var exploreSearxURL string
			var exploreMaxDepth int
			for _, j := range cfg.Consolidator.Scheduler.Jobs {
				if j.Task == "explore" {
					if u, ok := j.Config["searxng_url"].(string); ok {
						exploreSearxURL = u
					}
					if d, ok := j.Config["max_depth"].(int); ok {
						exploreMaxDepth = d
					}
					break
				}
			}

			features := []scheduler.Feature{
				rss.New(store.DB(), store),
				schedule.New(store.DB()),
				dyntools.New("/data/tools", registry, logger),
				mcp.NewFeature(mcpMgr, logger),
				topics.New(),
				explore.New(exploreSearxURL, llmClient, store, cfg.Agent.SystemPrompt, exploreMaxDepth),
				affinity.New(),
				forget.New(),
				preferences.New(store.DB()),
			}

			// Add location feature if enabled.
			locStore := do.MustInvoke[*location.Store](i)
			if locStore != nil {
				features = append(features, location.NewFeature(locStore))
				ag.SetLocationStore(locStore)
			}
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
		do.Provide(i, func(i do.Injector) (*schedule.Store, error) {
			store := do.MustInvoke[*memory.SQLiteStore](i)
			return schedule.NewStore(store.DB()), nil
		})

		// Admin server.
		do.Provide(i, func(i do.Injector) (*admin.Server, error) {
			cfg := do.MustInvoke[*config.Config](i)
			store := do.MustInvoke[*memory.SQLiteStore](i)
			userStore := do.MustInvoke[*user.SQLiteStore](i)
			schedStore := do.MustInvoke[*schedule.Store](i)
			logger := do.MustInvoke[*slog.Logger](i)
			return admin.NewServer(cfg.Admin, store, userStore, schedStore, logger), nil
		})

		// Scheduler (nil when disabled in config).
		do.Provide(i, provideScheduler)
	}
}

func provideScheduler(i do.Injector) (*scheduler.Scheduler, error) {
	cfg := do.MustInvoke[*config.Config](i)
	if !cfg.Consolidator.Scheduler.Enabled {
		return nil, nil
	}

	llmClient := do.MustInvoke[*llm.Client](i)
	store := do.MustInvoke[*memory.SQLiteStore](i)
	logger := do.MustInvoke[*slog.Logger](i)
	chatIface := do.MustInvoke[chat.Interface](i)
	userStore := do.MustInvoke[*user.SQLiteStore](i)
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
	cc := &scheduler.CronContext{
		LLM:             llmClient,
		Memory:          store,
		Notifier:        notifier,
		DB:              store.DB(),
		Logger:          logger,
		Users:           userStore,
		ChannelActivity: activityStore,
		MemoryAdmin:     store,
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
