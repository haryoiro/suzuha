package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/chat/discord"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/location"
	"github.com/haryoiro/suzuha/internal/mcpbridge"
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
	store := do.MustInvoke[*memory.SQLiteStore](injector)
	defer store.Close()

	_ = do.MustInvoke[*observe.Metrics](injector)

	mcpMgr := do.MustInvoke[*mcpbridge.Manager](injector)
	defer mcpMgr.Close()

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

	// Register Discord OnReady callback.
	dc := do.MustInvoke[*discord.Chat](injector)
	if dc != nil {
		registerDiscordOnReady(injector, dc)
	}

	// Start internal HTTP server.
	if cfg.Observe.MetricsAddr != "" {
		go startInternalHTTP(injector, cfgPath)
	}

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

func registerDiscordOnReady(injector do.Injector, dc *discord.Chat) {
	registry := do.MustInvoke[*tool.Registry](injector)
	userStore := do.MustInvoke[*user.SQLiteStore](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	logger := do.MustInvoke[*slog.Logger](injector)

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
		// Bot presence
		registry.Register(builtin.NewDiscordUpdateStatus(s))
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

		// Register /affinity slash command.
		affinityCmd, cmdErr := s.ApplicationCommandCreate(s.State.User.ID, "", &discordgo.ApplicationCommand{
			Name:        "affinity",
			Description: "あなたへの好感度を表示します（本人のみ表示）",
		})
		if cmdErr != nil {
			logger.Error("failed to register /affinity command", "error", cmdErr)
		} else {
			logger.Info("registered /affinity command", "id", affinityCmd.ID)
		}

		// Handle /affinity interaction.
		s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			if i.Type != discordgo.InteractionApplicationCommand {
				return
			}
			if i.ApplicationCommandData().Name != "affinity" {
				return
			}

			var discordUser *discordgo.User
			if i.Member != nil {
				discordUser = i.Member.User
			} else {
				discordUser = i.User
			}
			if discordUser == nil {
				return
			}

			u, resolveErr := userStore.Resolve(context.Background(), "discord", discordUser.ID, discordUser.Username)
			if resolveErr != nil {
				logger.Error("affinity command: resolve user", "error", resolveErr)
				if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "ユーザー情報の取得に失敗しました。",
						Flags:   discordgo.MessageFlagsEphemeral,
					},
				}); err != nil {
					logger.Warn("affinity command: respond error", "error", err)
				}
				return
			}

			events, affinityErr := userStore.GetAffinity(context.Background(), u.ID, 10)
			if affinityErr != nil {
				logger.Warn("affinity command: get affinity", "error", affinityErr)
			}

			displayName := u.DisplayName
			if displayName == "" {
				displayName = discordUser.Username
				if discordUser.GlobalName != "" {
					displayName = discordUser.GlobalName
				}
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "**%s の好感度**\n\n", displayName)
			fmt.Fprintf(&sb, "親密度: **%.1f**\n", u.Closeness)
			fmt.Fprintf(&sb, "信頼度: **%.1f**\n", u.Trust)
			fmt.Fprintf(&sb, "関心度: **%.1f**\n", u.Interest)

			if len(events) > 0 {
				sb.WriteString("\n**最近の変動**\n")
				axisNames := map[user.AffinityAxis]string{
					user.AxisCloseness: "親密",
					user.AxisTrust:     "信頼",
					user.AxisInterest:  "関心",
				}
				for _, e := range events {
					sign := "+"
					if e.Delta < 0 {
						sign = ""
					}
					fmt.Fprintf(&sb, "%s%.1f (%s) %s — %s\n",
						sign, e.Delta, axisNames[e.Axis], e.Reason, e.CreatedAt.Format("01/02"))
				}
			}

			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: sb.String(),
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}); err != nil {
				logger.Warn("affinity command: respond", "error", err)
			}
		})
	})
}

func startInternalHTTP(injector do.Injector, cfgPath string) {
	cfg := do.MustInvoke[*config.Config](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	logRing := do.MustInvoke[*observe.RingBuffer](injector)
	ag := do.MustInvoke[*agent.Agent](injector)
	channelStore := do.MustInvoke[*channel.Store](injector)
	sched := do.MustInvoke[*scheduler.Scheduler](injector)

	mux := http.NewServeMux()
	mux.Handle("/internal/logs", observe.LogHandler(logRing))
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
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		var taskCfg json.RawMessage
		if reqBody.Config != nil {
			taskCfg, _ = json.Marshal(reqBody.Config)
		} else {
			for _, j := range cfg.Consolidator.Scheduler.Jobs {
				if j.Task == taskName {
					taskCfg, _ = json.Marshal(j.Config)
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

	// LLM provider info / swap (persisted in app_settings).
	llmClient := do.MustInvoke[*llm.Client](injector)
	llmDB := do.MustInvokeNamed[*sql.DB](injector, "shared-db")

	// Restore saved provider on startup.
	{
		var savedJSON string
		err := llmDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'llm_provider'`).Scan(&savedJSON)
		if err == nil && savedJSON != "" {
			var saved struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				APIKey   string `json:"api_key"`
				APIBase  string `json:"api_base"`
				MaxCtx   int    `json:"max_ctx"`
				Vision   bool   `json:"vision"`
			}
			if json.Unmarshal([]byte(savedJSON), &saved) == nil && saved.Provider != "" {
				if swapErr := llmClient.SwapProvider(saved.Provider, saved.Model, saved.APIKey, saved.APIBase, saved.MaxCtx, saved.Vision); swapErr != nil {
					logger.Warn("failed to restore saved LLM provider", "error", swapErr)
				} else {
					if saved.MaxCtx > 0 {
						ag.AgentContext().SetMaxTokens(saved.MaxCtx)
					}
					logger.Info("restored saved LLM provider", "provider", saved.Provider, "model", saved.Model, "max_ctx", saved.MaxCtx)
				}
			}
		}
	}

	mux.HandleFunc("GET /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		prov, model, apiBase, vision := llmClient.ProviderInfo()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"provider": prov,
			"model":    model,
			"api_base": apiBase,
			"max_ctx":  llmClient.MaxContextTokens(),
			"vision":   vision,
		})
	})
	mux.HandleFunc("PUT /internal/llm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			APIKey   string `json:"api_key"`
			APIBase  string `json:"api_base"`
			MaxCtx   int    `json:"max_ctx"`
			Vision   bool   `json:"vision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.Provider == "" || body.Model == "" {
			http.Error(w, `{"error":"provider and model required"}`, http.StatusBadRequest)
			return
		}
		// Fall back to config defaults when api_key/api_base/max_ctx are omitted.
		if body.APIKey == "" {
			body.APIKey = cfg.LLM.APIKey
		}
		if body.APIBase == "" {
			body.APIBase = cfg.LLM.APIBase
		}
		if body.MaxCtx <= 0 {
			body.MaxCtx = cfg.LLM.MaxTokens
		}
		if err := llmClient.SwapProvider(body.Provider, body.Model, body.APIKey, body.APIBase, body.MaxCtx, body.Vision); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
			return
		}
		ag.AgentContext().SetMaxTokens(body.MaxCtx)
		// Force compaction if context exceeds new limit.
		if ag.AgentContext().UsageRatio() > 0.5 {
			compactCtx, compactCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			ag.ForceCompact(compactCtx)
			compactCancel()
			logger.Info("context compacted after provider switch", "messages", ag.AgentContext().Len(), "max_ctx", body.MaxCtx)
		} else {
			logger.Info("context max tokens updated", "max_ctx", body.MaxCtx)
		}
		// Persist selection.
		settingsJSON, _ := json.Marshal(body)
		_, _ = llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('llm_provider', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			string(settingsJSON),
		)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// Tool registry listing + enable/disable.
	registry := do.MustInvoke[*tool.Registry](injector)

	// Restore disabled tools on startup.
	{
		var disabledJSON string
		err := llmDB.QueryRow(`SELECT value FROM app_settings WHERE key = 'disabled_tools'`).Scan(&disabledJSON)
		if err == nil && disabledJSON != "" {
			var names []string
			if json.Unmarshal([]byte(disabledJSON), &names) == nil && len(names) > 0 {
				registry.SetDisabled(names)
				logger.Info("restored disabled tools", "count", len(names))
			}
		}
	}

	saveDisabledTools := func() {
		names := registry.DisabledNames()
		data, _ := json.Marshal(names)
		_, _ = llmDB.Exec(
			`INSERT INTO app_settings (key, value) VALUES ('disabled_tools', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			string(data),
		)
	}

	mux.HandleFunc("GET /internal/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := registry.All()
		type toolInfo struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Enabled     bool            `json:"enabled"`
		}
		out := make([]toolInfo, 0, len(tools))
		for _, t := range tools {
			out = append(out, toolInfo{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
				Enabled:     !registry.IsDisabled(t.Name()),
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": out})
	})
	mux.HandleFunc("PUT /internal/tools/{name}/enabled", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		// Update the disabled set.
		current := registry.DisabledNames()
		var updated []string
		if body.Enabled {
			for _, n := range current {
				if n != name {
					updated = append(updated, n)
				}
			}
		} else {
			found := false
			for _, n := range current {
				updated = append(updated, n)
				if n == name {
					found = true
				}
			}
			if !found {
				updated = append(updated, name)
			}
		}
		registry.SetDisabled(updated)
		saveDisabledTools()
		logger.Info("tool toggled", "tool", name, "enabled", body.Enabled)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

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

	logger.Info("internal server starting", "addr", cfg.Observe.MetricsAddr)
	if err := http.ListenAndServe(cfg.Observe.MetricsAddr, mux); err != nil {
		logger.Error("internal server failed", "error", err)
	}
}
