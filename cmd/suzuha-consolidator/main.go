package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/notification"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/explore"
	"github.com/haryoiro/suzuha/internal/rss"
	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/topics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config.
	cfgPath := "config.yaml"
	if p := os.Getenv("SUZUHA_CONFIG"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := observe.NewLogger(cfg.Observe.LogLevel)

	// Setup LLM client for consolidation (before memory store so we can wire embedFn).
	llmClient, err := llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase,
		cfg.LLM.MaxTokens,
		llm.EmbeddingConfig{
			Provider: cfg.Embedding.Provider,
			Model:    cfg.Embedding.Model,
			APIKey:   cfg.Embedding.APIKey,
			APIBase:  cfg.Embedding.APIBase,
			Dims:     cfg.Embedding.Dims,
		},
		nil, logger,
	)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	// Setup memory store with embedding function.
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return llmClient.Embed(ctx, text)
	}
	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath, embedFn, false)
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	defer store.Close()

	srv := consolidator.NewServer(llmClient, store, logger)

	// Start gRPC server.
	lis, err := net.Listen("tcp", cfg.Consolidator.Address)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Consolidator.Address, err)
	}

	grpcServer := grpc.NewServer()
	consolidator.NewGRPCServer(srv).Register(grpcServer)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Setup scheduler if enabled.
	var sched *scheduler.Scheduler
	if cfg.Consolidator.Scheduler.Enabled {
		// Connect to agent notification service.
		var notifier notification.Notifier
		agentConn, dialErr := grpc.NewClient(
			cfg.Consolidator.AgentNotify,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if dialErr != nil {
			logger.Warn("scheduler: agent notification unavailable, notifications disabled", "error", dialErr)
			notifier = notification.NopNotifier{}
		} else {
			defer agentConn.Close()
			notifier = notification.NewGRPCNotifier(agentConn)
			logger.Info("scheduler: agent notification connected", "address", cfg.Consolidator.AgentNotify)
		}

		// Resolve scheduler-level timezone.
		schedulerLoc := time.UTC
		if tz := cfg.Consolidator.Scheduler.Timezone; tz != "" {
			if parsed, tzErr := time.LoadLocation(tz); tzErr == nil {
				schedulerLoc = parsed
			} else {
				logger.Warn("scheduler: invalid timezone, using UTC", "timezone", tz, "error", tzErr)
			}
		}

		// Wrap notifier with quiet hours middleware if configured.
		if cfg.Consolidator.Scheduler.QuietHours.Enabled {
			notifier = notification.WithQuietHours(notification.QuietHoursConfig{
				Start:    cfg.Consolidator.Scheduler.QuietHours.Start,
				End:      cfg.Consolidator.Scheduler.QuietHours.End,
				Location: schedulerLoc,
			}, logger)(notifier)
			logger.Info("scheduler: quiet hours enabled",
				"start", cfg.Consolidator.Scheduler.QuietHours.Start,
				"end", cfg.Consolidator.Scheduler.QuietHours.End,
				"timezone", schedulerLoc.String(),
			)
		}

		// Register features.
		features := []scheduler.Feature{
			rss.New(store.DB(), store),
			topics.New(),
			explore.New(),
			schedule.New(store.DB()),
		}

		taskRegistry := scheduler.NewRegistry()
		for _, f := range features {
			if setupErr := f.Setup(ctx, store.DB()); setupErr != nil {
				return fmt.Errorf("feature %s setup: %w", f.Name(), setupErr)
			}
			for _, t := range f.Tasks() {
				taskRegistry.Register(t)
			}
		}

		// Build CronContext with shared services.
		cc := &scheduler.CronContext{
			LLM:          llmClient,
			Memory:       store,
			Notifier:     notifier,
			DB:           store.DB(),
			Logger:       logger,
			Timezone:     schedulerLoc,
			SystemPrompt: cfg.Agent.SystemPrompt,
		}

		sched = scheduler.New(taskRegistry, cc, logger)

		if setupErr := sched.Setup(ctx); setupErr != nil {
			return fmt.Errorf("scheduler setup: %w", setupErr)
		}

		// Convert config jobs to scheduler JobDefs.
		jobDefs := make([]scheduler.JobDef, len(cfg.Consolidator.Scheduler.Jobs))
		for i, j := range cfg.Consolidator.Scheduler.Jobs {
			jobDefs[i] = scheduler.JobDef{
				Name:   j.Name,
				Task:   j.Task,
				Cron:   j.Cron,
				Config: j.Config,
			}
		}
		if loadErr := sched.LoadJobs(jobDefs); loadErr != nil {
			return fmt.Errorf("scheduler load jobs: %w", loadErr)
		}

		sched.Start()
		logger.Info("scheduler started", "jobs", len(cfg.Consolidator.Scheduler.Jobs))
	}

	go func() {
		<-ctx.Done()
		logger.Info("suzuha-consolidator shutting down")
		if sched != nil {
			sched.Stop()
		}
		grpcServer.GracefulStop()
	}()

	logger.Info("suzuha-consolidator started", "address", cfg.Consolidator.Address)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}
