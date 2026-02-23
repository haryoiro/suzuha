package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/cli"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	// Setup observability.
	logRing := observe.NewRingBuffer(1000)
	logger := observe.NewLoggerWithRing(cfg.Observe.LogLevel, logRing)
	metrics := observe.NewMetrics(prometheus.DefaultRegisterer)

	// Setup memory store with embedding function.
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		// TODO: Implement embedding via LLM client.
		return nil, nil
	}
	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath, embedFn, true)
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	defer store.Close()

	// Setup LLM client.
	llmClient, err := llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey,
		cfg.LLM.MaxTokens, metrics, logger,
	)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	// Setup event bus.
	bus := event.NewBus(128)

	// Setup tool registry.
	registry := tool.NewRegistry()
	registry.Register(builtin.NewFetch())

	// Setup chat interface: Discord if token is set, otherwise CLI.
	var chatIface chat.Interface
	if cfg.Discord.Token != "" {
		dc := discord.New(cfg.Discord.Token, cfg.Discord.BotID, bus, logger)
		dc.OnReady(func(s *discordgo.Session) {
			registry.Register(builtin.NewDiscordReact(s))
			registry.Register(builtin.NewDiscordReply(s))
			registry.Register(builtin.NewDiscordGetHistory(s))
			logger.Info("discord tools registered")
		})
		chatIface = dc
		logger.Info("chat mode: discord")
	} else {
		chatIface = cli.New(os.Stdin, os.Stdout, bus)
		logger.Info("chat mode: cli")
	}

	// Create agent.
	ag := agent.New(
		agent.Config{
			SystemPrompt:     cfg.Agent.SystemPrompt,
			BotID:            cfg.Discord.BotID,
			ContextWindowPct: cfg.Agent.ContextWindowPct,
			MaxContextTokens: cfg.LLM.MaxTokens,
		},
		llmClient, registry, store, bus, chatIface,
		nil, // consolidator client — nil for now
		logger, metrics,
	)

	// Start metrics server.
	if cfg.Observe.MetricsAddr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			mux.Handle("/internal/logs", observe.LogHandler(logRing))
			logger.Info("metrics server starting", "addr", cfg.Observe.MetricsAddr)
			if err := http.ListenAndServe(cfg.Observe.MetricsAddr, mux); err != nil {
				logger.Error("metrics server failed", "error", err)
			}
		}()
	}

	// Context with signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
