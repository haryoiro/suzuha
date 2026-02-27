package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"github.com/haryoiro/suzuha/internal/rss"
	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/tool/builtin"
	"github.com/haryoiro/suzuha/internal/user"
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

	// Setup memory store first (runs migrations, provides DB for metrics).
	// embedFn is wired via closure after LLM client is created below.
	var llmClient *llm.Client
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return llmClient.Embed(ctx, text)
	}
	store, err := memory.NewSQLiteStore(cfg.Memory.DBPath, embedFn, true)
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	defer store.Close()

	// Setup metrics (SQLite-backed, persists across restarts).
	metrics := observe.NewMetrics(store.DB())

	// Setup LLM client.
	llmClient, err = llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase,
		cfg.LLM.MaxTokens,
		llm.EmbeddingConfig{
			Provider: cfg.Embedding.Provider,
			Model:    cfg.Embedding.Model,
			APIKey:   cfg.Embedding.APIKey,
			APIBase:  cfg.Embedding.APIBase,
			Dims:     cfg.Embedding.Dims,
		},
		metrics, logger,
	)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	// Setup event bus.
	bus := event.NewBus(128)

	// Setup user store (shares DB with memory store).
	// Pass bot's platform user ID so it can be marked as is_bot on creation.
	userStore := user.NewSQLiteStore(store.DB(), cfg.Discord.BotID)

	// Setup tool registry.
	registry := tool.NewRegistry()
	registry.Register(builtin.NewFetch())

	// Setup chat interface: Discord if token is set, otherwise CLI.
	var chatIface chat.Interface
	var dc *discord.Chat
	if cfg.Discord.Token != "" {
		dc = discord.New(cfg.Discord.Token, cfg.Discord.BotID, bus, logger)
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
		consolClient, store.DB(),
		logger, metrics,
	)

	// Register user profile tool (needs agent context for short-term memory update).
	registry.Register(builtin.NewUpdateUserProfile(userStore, func(userID, newName string) {
		ag.AgentContext().UpdateUserName(userID, newName)
	}))

	// Register features (RSS tools, etc.).
	features := []scheduler.Feature{
		rss.New(store.DB(), store),
		schedule.New(store.DB()),
	}
	for _, f := range features {
		if err := f.Setup(context.Background(), store.DB()); err != nil {
			logger.Error("feature setup failed", "feature", f.Name(), "error", err)
		}
		for _, t := range f.Tools() {
			registry.Register(t)
		}
	}

	// Register Discord OnReady callback (after agent is created so closure can reference ag).
	if dc != nil {
		dc.OnReady(func(s *discordgo.Session) {
			// Register Discord-specific tools.
			registry.Register(builtin.NewDiscordReact(s))
			registry.Register(builtin.NewDiscordReply(s))
			registry.Register(builtin.NewDiscordGetHistory(s))
			registry.Register(builtin.NewDiscordSendDM(s))
			// Channel management
			registry.Register(builtin.NewDiscordCreateChannel(s))
			registry.Register(builtin.NewDiscordEditChannel(s))
			registry.Register(builtin.NewDiscordDeleteChannel(s))
			registry.Register(builtin.NewDiscordListChannels(s))
			// Member management
			registry.Register(builtin.NewDiscordKickMember(s))
			registry.Register(builtin.NewDiscordBanMember(s))
			registry.Register(builtin.NewDiscordTimeoutMember(s))
			registry.Register(builtin.NewDiscordListMembers(s))
			// Message management
			registry.Register(builtin.NewDiscordDeleteMessage(s))
			registry.Register(builtin.NewDiscordPinMessage(s))
			// Role management
			registry.Register(builtin.NewDiscordAddRole(s))
			registry.Register(builtin.NewDiscordRemoveRole(s))
			registry.Register(builtin.NewDiscordListRoles(s))
			// Server & threads
			registry.Register(builtin.NewDiscordServerInfo(s))
			registry.Register(builtin.NewDiscordCreateThread(s))
			logger.Info("discord tools registered")

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

	// Start internal HTTP server.
	if cfg.Observe.MetricsAddr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/internal/logs", observe.LogHandler(logRing))
			mux.HandleFunc("POST /internal/compact", func(w http.ResponseWriter, r *http.Request) {
				ag.ForceCompact(r.Context())
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"ok":true,"message_count":%d}`, ag.AgentContext().Len())
			})
			mux.HandleFunc("POST /internal/reload-prompt", func(w http.ResponseWriter, r *http.Request) {
			// Re-read prompt files from disk and update agent's system prompt.
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
				})
			})
			logger.Info("internal server starting", "addr", cfg.Observe.MetricsAddr)
			if err := http.ListenAndServe(cfg.Observe.MetricsAddr, mux); err != nil {
				logger.Error("internal server failed", "error", err)
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
