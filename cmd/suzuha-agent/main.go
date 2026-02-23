package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"net"

	"github.com/bwmarrin/discordgo"
	pb "github.com/haryoiro/suzuha/gen/notification/v1"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/cli"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/notification"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
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

	// Setup LLM client (before memory store so we can wire embedFn).
	llmClient, err := llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase,
		cfg.LLM.MaxTokens, cfg.LLM.EmbeddingModel, cfg.LLM.EmbeddingDims,
		metrics, logger,
	)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	// Setup memory store with embedding function.
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return llmClient.Embed(ctx, text)
	}
	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath, embedFn, true)
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	defer store.Close()

	// Setup event bus.
	bus := event.NewBus(128)

	// Setup user store (shares DB with memory store).
	userStore := user.NewSQLiteStore(store.DB())

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
			registry.Register(builtin.NewDiscordSendDM(s))
			logger.Info("discord tools registered")
		})
		chatIface = dc
		logger.Info("chat mode: discord")
	} else {
		chatIface = cli.New(os.Stdin, os.Stdout, bus)
		logger.Info("chat mode: cli")
	}

	// Connect to consolidator (graceful: nil fallback if unavailable).
	var consolClient consolidator.Client
	consolGRPC, err := consolidator.NewGRPCClient(cfg.Consolidator.Address)
	if err != nil {
		logger.Warn("consolidator connection failed, compaction will use truncation fallback", "error", err)
	} else {
		consolClient = consolGRPC
		defer consolGRPC.Close()
		logger.Info("consolidator connected", "address", cfg.Consolidator.Address)
	}

	// Create agent.
	ag := agent.New(
		agent.Config{
			SystemPrompt:     cfg.Agent.SystemPrompt,
			BotID:            cfg.Discord.BotID,
			ContextWindowPct: cfg.Agent.ContextWindowPct,
			MaxContextTokens: cfg.LLM.MaxTokens,
		},
		llmClient, registry, store, userStore, bus, chatIface,
		consolClient,
		logger, metrics,
	)

	// Register user profile tool (needs agent context for short-term memory update).
	registry.Register(builtin.NewUpdateUserProfile(userStore, func(userID, newName string) {
		ag.AgentContext().UpdateUserName(userID, newName)
	}))

	// Register RSS tools.
	registry.Register(builtin.NewRSSSubscribe(store.DB()))
	registry.Register(builtin.NewRSSUnsubscribe(store.DB()))
	registry.Register(builtin.NewRSSList(store.DB()))
	registry.Register(builtin.NewRSSPreference(store))

	// Start metrics server.
	if cfg.Observe.MetricsAddr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			mux.Handle("/internal/logs", observe.LogHandler(logRing))
			mux.HandleFunc("POST /internal/compact", func(w http.ResponseWriter, r *http.Request) {
				ag.ForceCompact(r.Context())
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"ok":true,"message_count":%d}`, ag.AgentContext().Len())
			})
			mux.HandleFunc("GET /internal/context", func(w http.ResponseWriter, r *http.Request) {
				actx := ag.AgentContext()
				msgs := actx.Messages()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"messages":         msgs,
					"count":            len(msgs),
					"estimated_tokens": actx.EstimatedTokens(),
					"usage_ratio":      actx.UsageRatio(),
					"max_tokens":       actx.MaxTokens(),
				})
			})
			logger.Info("metrics server starting", "addr", cfg.Observe.MetricsAddr)
			if err := http.ListenAndServe(cfg.Observe.MetricsAddr, mux); err != nil {
				logger.Error("metrics server failed", "error", err)
			}
		}()
	}

	// Start notification gRPC server for consolidator → agent notifications.
	if cfg.Consolidator.AgentNotify != "" {
		notifServer := notification.NewServer(chatIface, logger)
		grpcServer := grpc.NewServer()
		pb.RegisterNotificationServiceServer(grpcServer, notifServer)

		lis, lisErr := net.Listen("tcp", cfg.Consolidator.AgentNotify)
		if lisErr != nil {
			return fmt.Errorf("notification listen %s: %w", cfg.Consolidator.AgentNotify, lisErr)
		}
		go func() {
			logger.Info("notification gRPC server starting", "addr", cfg.Consolidator.AgentNotify)
			if srvErr := grpcServer.Serve(lis); srvErr != nil {
				logger.Error("notification gRPC server failed", "error", srvErr)
			}
		}()
		defer grpcServer.GracefulStop()
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
