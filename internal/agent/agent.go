package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/chat"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/user"
)

// Agent is the main event loop that processes events, calls the LLM,
// executes tools, and sends responses.
type Agent struct {
	ctx     *Context
	llm     *llm.Client
	tools   *tool.Registry
	memory  memory.Store
	users   user.Store
	bus     *event.Bus
	chat    chat.Interface
	consol  consolidator.Client
	db              *sql.DB // shared DB for channel activity tracking
	channelSettings *channelpkg.Store
	logger          *slog.Logger
	metrics         *observe.Metrics

	systemPrompt     string
	botID            string
	contextWindowPct float64
}

// Config holds agent configuration.
type Config struct {
	SystemPrompt     string
	BotID            string
	ContextWindowPct float64 // trigger compaction at this ratio (e.g. 0.8)
	MaxContextTokens int
}

// New creates an Agent.
func New(
	cfg Config,
	llmClient *llm.Client,
	tools *tool.Registry,
	memStore memory.Store,
	userStore user.Store,
	bus *event.Bus,
	chatIface chat.Interface,
	consolClient consolidator.Client,
	db *sql.DB,
	channelSettings *channelpkg.Store,
	logger *slog.Logger,
	metrics *observe.Metrics,
) *Agent {
	agentCtx := NewContext(cfg.MaxContextTokens)

	// System prompt is stored separately — immune to compaction/truncation.
	agentCtx.SetSystemPrompt(cfg.SystemPrompt)

	// Try to restore context from previous session.
	if saved := loadContext(db, logger); len(saved) > 0 {
		// Backward compat: strip system prompt from messages if present.
		if saved[0].Role == "system" {
			saved = saved[1:]
		}
		agentCtx.ReplaceAll(saved)
		logger.Info("context restored from db", "messages", len(saved))
	}

	return &Agent{
		ctx:              agentCtx,
		llm:              llmClient,
		tools:            tools,
		memory:           memStore,
		users:            userStore,
		bus:              bus,
		chat:             chatIface,
		consol:           consolClient,
		db:               db,
		channelSettings:  channelSettings,
		logger:           logger,
		metrics:          metrics,
		systemPrompt:     cfg.SystemPrompt,
		botID:            cfg.BotID,
		contextWindowPct: cfg.ContextWindowPct,
	}
}

// AgentContext returns the agent's context for external use (e.g. tool callbacks).
func (a *Agent) AgentContext() *Context {
	return a.ctx
}

// SetBotID updates the bot's platform user ID at runtime.
// Used when the actual ID is only known after platform connection (e.g. Discord).
func (a *Agent) SetBotID(id string) {
	a.botID = id
}

// Run starts the agent event loop. Blocks until ctx is canceled.
// When multiple events arrive while the LLM is processing, they are drained
// from the buffer and processed as a batch: all messages are ingested into
// context, but only one LLM response is generated for the entire batch.
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-events:
			// Drain any buffered events to process as a batch.
			batch := []event.Event{evt}
		drain:
			for {
				select {
				case e := <-events:
					batch = append(batch, e)
				default:
					break drain
				}
			}

			if a.metrics != nil {
				for _, e := range batch {
					a.metrics.EventsTotal.WithLabelValues(e.Source, e.Type).Inc()
				}
			}

			if len(batch) > 1 {
				a.logger.Info("batch processing", "batch_size", len(batch))
			}

			if err := a.handleBatch(ctx, batch); err != nil {
				a.logger.Error("handle event failed", "error", err)
			}
		}
	}
}

// ingestEvent processes a single event: resolves the user, adds the message
// to context, and injects user profile. It does NOT trigger LLM completion.
// Returns the message and the user's affinity score.
func (a *Agent) ingestEvent(ctx context.Context, evt event.Event) (llm.Message, float64, float64) {
	msg := eventToMessage(evt)

	a.logger.Info("event received",
		"source", evt.Source, "type", evt.Type,
		"user", msg.UserName, "user_id", msg.UserID,
		"channel", msg.Channel, "content", truncate(msg.Content, 100))

	// Resolve user identity (auto-create if not exists).
	var userCloseness, userInterest float64
	if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
		u, err := a.users.Resolve(ctx, msg.Source, msg.UserID, msg.UserName)
		if err != nil {
			a.logger.Warn("user resolve failed", "error", err)
		} else {
			if u.DisplayName != "" {
				msg.UserName = u.DisplayName
				a.logger.Debug("user resolved", "display_name", u.DisplayName, "role", u.Role,
					"closeness", u.Closeness, "trust", u.Trust, "interest", u.Interest)
			}
			userCloseness = u.Closeness
			userInterest = u.Interest
			guildID, _ := evt.Payload["guild_id"].(string)
			guildName, _ := evt.Payload["guild_name"].(string)
			channelName, _ := evt.Payload["channel_name"].(string)
			if guildID != "" {
				if err := a.users.TrackGuildChannel(ctx, u.ID, guildID, guildName, msg.Channel, channelName); err != nil {
					a.logger.Warn("track guild channel failed", "error", err)
				}
			}
		}
	}

	// Track channel activity for topic backoff (non-bot messages only).
	if msg.Channel != "" && a.db != nil && msg.UserID != a.botID {
		_, _ = a.db.ExecContext(ctx,
			`INSERT INTO channel_activity (channel_id, last_user_message_at) VALUES (?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET last_user_message_at = excluded.last_user_message_at`,
			msg.Channel, time.Now())
	}

	// Bootstrap channel history if this is a new channel.
	a.injectChannelHistory(ctx, msg.Channel, msg.Content, msg.Source)

	// Add to context.
	a.ctx.Add(msg)

	return msg, userCloseness, userInterest
}

// handleBatch ingests all events in the batch, then generates a single LLM
// response. If any event in the batch is directly addressed, [RESPOND] is used;
// otherwise the directive is based on the last event's affinity.
func (a *Agent) handleBatch(ctx context.Context, batch []event.Event) error {
	// 0. Filter out disabled channels.
	if a.channelSettings != nil {
		var filtered []event.Event
		for _, evt := range batch {
			chID, _ := evt.Payload["channel"].(string)
			isDM, _ := evt.Payload["is_dm"].(bool)
			if chID != "" && !isDM && a.channelSettings.GetMode(chID) == channelpkg.ModeDisabled {
				a.logger.Debug("skipping disabled channel", "channel", chID)
				continue
			}
			filtered = append(filtered, evt)
		}
		if len(filtered) == 0 {
			return nil
		}
		batch = filtered
	}

	// 1. Ingest all events into context.
	var lastMsg llm.Message
	var lastEvt event.Event
	var userCloseness, userInterest float64
	var directlyAddressed bool
	for _, evt := range batch {
		msg, closeness, interest := a.ingestEvent(ctx, evt)
		lastMsg = msg
		lastEvt = evt
		userCloseness = closeness
		userInterest = interest
		if isDirectlyAddressed(evt, a.botID) {
			directlyAddressed = true
		}
	}

	// 2. Check context window usage — compact if needed.
	ratio := a.ctx.UsageRatio()
	a.logger.Debug("context window", "usage_ratio", fmt.Sprintf("%.2f", ratio), "message_count", len(a.ctx.Messages()))
	if a.contextWindowPct > 0 && ratio > a.contextWindowPct {
		a.logger.Info("context compaction triggered", "ratio", fmt.Sprintf("%.2f", ratio))
		a.compact(ctx)
	}

	// 3. Build ephemeral context (memories + profiles — not persisted).
	//    Run memory search and user profile building in parallel to reduce
	//    the total latency from sequential embedding API calls.
	var (
		memMsg   string
		profiles []llm.Message
		wg       sync.WaitGroup
	)
	if a.memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			memMsg = a.buildMemoryContext(ctx, lastMsg.Content)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		profiles = a.buildUserProfiles(ctx)
	}()
	wg.Wait()

	var ephemeral []llm.Message
	if memMsg != "" {
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: memMsg, Timestamp: time.Now(),
		})
	}
	if len(profiles) > 0 {
		ephemeral = append(ephemeral, profiles...)
	}

	// 4. Check listen mode — ingest into context but skip LLM response.
	respondChannel, _ := lastEvt.Payload["channel"].(string)
	isDM, _ := lastEvt.Payload["is_dm"].(bool)
	if a.channelSettings != nil && respondChannel != "" && !isDM {
		mode := a.channelSettings.GetMode(respondChannel)
		if mode == channelpkg.ModeListen {
			a.logger.Info("listen mode: ingesting without response", "channel", respondChannel)
			persistContext(ctx, a.db, a.ctx, a.logger)
			return nil
		}
		// Home channel: the bot's own space — feel free to be yourself.
		if a.channelSettings.Get(respondChannel).Home {
			ephemeral = append(ephemeral, llm.Message{
				Role:      "system",
				Content:   "ここは自分の住処チャンネルです。リラックスして自由に話して。",
				Timestamp: time.Now(),
			})
		}
	}

	// 5. Determine response directive.
	// If any event in the batch was directly addressed, force RESPOND
	// regardless of which event was last.
	var directive string
	if directlyAddressed {
		directive = "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
	} else {
		directive = responseDirective(lastEvt, a.botID, userCloseness, userInterest)
	}
	a.logger.Info("llm request", "message_count", len(a.ctx.Messages()),
		"ephemeral_count", len(ephemeral), "directive", directive, "batch_size", len(batch))

	// 6. LLM completion with tool loop.
	channel, _ := lastEvt.Payload["channel"].(string)
	resp, err := a.completeWithTools(ctx, directive, channel, ephemeral)
	if err != nil {
		return fmt.Errorf("agent: complete: %w", err)
	}

	// 7. Add assistant response to context.
	a.ctx.Add(llm.Message{
		Role:      "assistant",
		Content:   resp.Text,
		Channel:   channel,
		Timestamp: time.Now(),
		ToolCalls: resp.ToolCalls,
	})

	// 8. Send response (strip LLM thinking tags, directive tags, and silent markers).
	text := stripDirectiveTags(stripThinkTags(resp.Text))
	if isSilentResponse(text) {
		a.logger.Info("skipping response (silent)",
			"raw_text", truncate(resp.Text, 100))
	} else if a.channelSettings != nil && respondChannel != "" && !isDM &&
		a.channelSettings.GetMode(respondChannel) != channelpkg.ModeActive {
		a.logger.Info("suppressing send to non-active channel",
			"channel", respondChannel, "mode", string(a.channelSettings.GetMode(respondChannel)))
	} else {
		a.logger.Info("sending response",
			"channel", channel,
			"length", len(text),
			"content", truncate(text, 200))
		if err := a.chat.Send(ctx, channel, text); err != nil {
			return fmt.Errorf("agent: send: %w", err)
		}
	}

	// 9. Persist context to DB (survives restarts).
	persistContext(ctx, a.db, a.ctx, a.logger)

	return nil
}

// completeWithTools runs the LLM and executes tool calls in a loop.
// The directive and ephemeral messages are appended as transient system messages
// at the end of the message list for the first LLM call only; they are not
// persisted in context.
func (a *Agent) completeWithTools(ctx context.Context, directive, channel string, ephemeral []llm.Message) (*llm.Response, error) {
	allTools := a.tools.All()
	maxIter := 10

	for iter := range maxIter {
		// Send typing indicator on each iteration (Discord typing expires after ~10s).
		if channel != "" {
			if typer, ok := a.chat.(chat.Typer); ok {
				typer.Typing(ctx, channel)
			}
		}

		msgs := a.ctx.MessagesWithSystem()
		// Inject ephemeral context + directive only on the first iteration.
		if iter == 0 {
			msgs = append(msgs, ephemeral...)
			if directive != "" {
				msgs = append(msgs, llm.Message{
					Role:      "system",
					Content:   directive,
					Timestamp: time.Now(),
				})
			}
		}
		resp, err := a.llm.Complete(ctx, msgs, allTools)
		if err != nil {
			return nil, err
		}

		a.logger.Info("llm response",
			"iteration", iter,
			"finish_reason", resp.FinishReason,
			"text_length", len(resp.Text),
			"tool_calls", len(resp.ToolCalls),
			"tokens_in", resp.Usage.PromptTokens,
			"tokens_out", resp.Usage.CompletionTokens,
			"content", truncate(resp.Text, 200))

		if !resp.HasToolCalls() {
			return resp, nil
		}

		// Add assistant message with tool calls.
		a.ctx.Add(llm.Message{
			Role:      "assistant",
			Content:   resp.Text,
			Channel:   channel,
			Timestamp: time.Now(),
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			a.logger.Info("tool call",
				"iteration", iter,
				"tool", tc.Function.Name,
				"call_id", tc.ID,
				"args", truncate(tc.Function.Arguments, 200))

			t, ok := a.tools.Get(tc.Function.Name)
			if !ok {
				a.logger.Warn("unknown tool", "tool", tc.Function.Name)
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: unknown tool %q", tc.Function.Name),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
				})
				continue
			}

			if a.metrics != nil {
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "called").Inc()
			}

			start := time.Now()
			result, err := t.Execute(ctx, json.RawMessage(tc.Function.Arguments))
			elapsed := time.Since(start)

			if err != nil {
				a.logger.Error("tool execute error",
					"tool", tc.Function.Name, "error", err, "elapsed_ms", elapsed.Milliseconds())
				if a.metrics != nil {
					a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, "error").Inc()
				}
				a.ctx.Add(llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("error: %v", err),
					ToolCallID: tc.ID,
					Timestamp:  time.Now(),
				})
				continue
			}

			if a.metrics != nil {
				status := "success"
				if result.IsError {
					status = "error"
				}
				a.metrics.ToolCallsTotal.WithLabelValues(tc.Function.Name, status).Inc()
			}

			// Serialize tool result as content string.
			content := ""
			for _, c := range result.Content {
				content += c.Text
			}

			a.logger.Info("tool result",
				"tool", tc.Function.Name,
				"elapsed_ms", elapsed.Milliseconds(),
				"is_error", result.IsError,
				"result", truncate(content, 200))

			a.ctx.Add(llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
				Timestamp:  time.Now(),
			})
		}
	}

	return nil, fmt.Errorf("agent: tool loop exceeded %d iterations", maxIter)
}

// ReloadPrompt updates the system prompt.
// Called when prompt files are edited via the admin dashboard.
func (a *Agent) ReloadPrompt(newPrompt string) {
	a.systemPrompt = newPrompt
	a.ctx.SetSystemPrompt(newPrompt)
	a.logger.Info("system prompt reloaded", "length", len(newPrompt))
}

// ForceCompact triggers context compaction externally (e.g. from admin API).
func (a *Agent) ForceCompact(ctx context.Context) {
	a.compact(ctx)
}

func (a *Agent) compact(ctx context.Context) {
	// System prompt is stored separately and immune to compaction.
	// Only conversation messages are subject to compaction.
	msgs := a.ctx.Messages()
	target := len(msgs) / 2

	if a.consol != nil {
		result, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
			Messages:    msgs,
			TargetCount: target,
		})
		if err != nil {
			a.logger.Warn("consolidator compact failed, falling back to truncation", "error", err)
			a.ctx.TruncateOldest(len(msgs) / 2)
			a.ctx.ResetInjectedUsers()
			a.ctx.ResetSeenChannels()
			persistContext(ctx, a.db, a.ctx, a.logger)
			return
		}

		var kept []llm.Message
		for _, idx := range result.KeepIndices {
			if idx >= 0 && idx < len(msgs) {
				kept = append(kept, msgs[idx])
			}
		}
		a.ctx.ReplaceAll(kept)
		a.ctx.ResetInjectedUsers()
		a.ctx.ResetSeenChannels()
		persistContext(ctx, a.db, a.ctx, a.logger)

		a.applyAffinityDeltas(ctx, result.AffinityDeltas, msgs)
		return
	}

	// No consolidator available — simple truncation fallback.
	a.ctx.TruncateOldest(len(msgs) / 2)
	a.ctx.ResetInjectedUsers()
	a.ctx.ResetSeenChannels()
	persistContext(ctx, a.db, a.ctx, a.logger)
}

// applyAffinityDeltas records affinity changes and updates user scores.
func (a *Agent) applyAffinityDeltas(ctx context.Context, deltas []consolidator.AffinityDelta, originalMsgs []llm.Message) {
	if a.users == nil || len(deltas) == 0 {
		return
	}
	for _, d := range deltas {
		u, err := a.users.Resolve(ctx, d.Platform, d.PlatformUserID, "")
		if err != nil {
			a.logger.Warn("user resolve for affinity failed", "error", err)
			continue
		}

		// Collect message IDs and time range from the original messages.
		var interactionIDs []string
		var groupStart, groupEnd time.Time
		for _, idx := range d.MessageIndices {
			if idx >= 0 && idx < len(originalMsgs) {
				m := originalMsgs[idx]
				if m.MessageID != "" {
					interactionIDs = append(interactionIDs, m.MessageID)
				}
				if groupStart.IsZero() || m.Timestamp.Before(groupStart) {
					groupStart = m.Timestamp
				}
				if groupEnd.IsZero() || m.Timestamp.After(groupEnd) {
					groupEnd = m.Timestamp
				}
			}
		}

		evt := &user.AffinityEvent{
			UserID:         u.ID,
			Delta:          d.Delta,
			Axis:           user.AffinityAxis(d.Axis),
			Reason:         d.Reason,
			InteractionIDs: interactionIDs,
			GroupStart:     groupStart,
			GroupEnd:       groupEnd,
		}
		if err := a.users.UpdateAffinity(ctx, evt); err != nil {
			a.logger.Warn("update affinity failed", "error", err)
		}
	}
}

// buildUserProfiles collects ephemeral profile messages for all users
// seen in the current context who haven't been profiled yet.
// Returns system messages that are NOT persisted in context.
func (a *Agent) buildUserProfiles(ctx context.Context) []llm.Message {
	if a.users == nil {
		return nil
	}

	// Collect unique (platform, userID) pairs from recent context messages.
	type userKey struct{ platform, userID string }
	seen := make(map[userKey]bool)
	var keys []userKey
	for _, m := range a.ctx.Messages() {
		if m.UserID == "" || m.UserID == a.botID || m.Role != "user" {
			continue
		}
		k := userKey{m.Source, m.UserID}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	// Build all user profiles + self-reflection in parallel.
	// Each profile may call embedding API, so parallelism reduces total latency.
	type indexedMsg struct {
		index   int
		content string
	}
	results := make([]indexedMsg, 0, len(keys)+1)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, k := range keys {
		wg.Add(1)
		go func(idx int, platform, userID string) {
			defer wg.Done()
			content := a.buildUserProfile(ctx, platform, userID)
			if content != "" {
				mu.Lock()
				results = append(results, indexedMsg{idx, content})
				mu.Unlock()
			}
		}(i, k.platform, k.userID)
	}

	// Self-reflection memories (also uses embedding).
	if a.memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			selfMems, err := a.memory.SearchByType(ctx, "", memory.MemoryTypeSelf, 3)
			if err == nil && len(selfMems) > 0 {
				var sb strings.Builder
				sb.WriteString("Self-awareness:\n")
				for _, m := range selfMems {
					fmt.Fprintf(&sb, "  - %s\n", m.Content)
				}
				mu.Lock()
				results = append(results, indexedMsg{len(keys), sb.String()})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Sort by original index to keep stable ordering.
	slices.SortFunc(results, func(a, b indexedMsg) int { return a.index - b.index })

	msgs := make([]llm.Message, 0, len(results))
	for _, r := range results {
		msgs = append(msgs, llm.Message{
			Role: "system", Content: r.content, Timestamp: time.Now(),
		})
	}
	return msgs
}

// buildUserProfile builds a profile summary string for a single user.
// Returns "" if the user cannot be resolved.
func (a *Agent) buildUserProfile(ctx context.Context, platform, platformUserID string) string {
	u, err := a.users.Resolve(ctx, platform, platformUserID, "")
	if err != nil {
		a.logger.Debug("user profile lookup failed", "error", err)
		return ""
	}

	content := fmt.Sprintf("[User profile: %s (ID=%s) role=%s closeness=%.2f trust=%.2f interest=%.2f]\n",
		u.DisplayName, u.ID, u.Role, u.Closeness, u.Trust, u.Interest)

	// Fetch recent affinity history.
	events, err := a.users.GetAffinity(ctx, u.ID, 5)
	if err != nil {
		a.logger.Debug("affinity history lookup failed", "error", err)
	}
	if len(events) > 0 {
		content += "Recent affinity history:\n"
		for _, e := range events {
			content += fmt.Sprintf("  %+.1f (%s): %s (%s)\n", e.Delta, e.Axis, e.Reason, e.GroupEnd.Format("2006-01-02"))
		}
	}

	// Fetch user-type memories.
	if a.memory != nil {
		memories, err := a.memory.ListByUser(ctx, u.ID, 5)
		if err != nil {
			a.logger.Debug("user memory search failed", "error", err)
		}
		if len(memories) > 0 {
			content += "Known facts:\n"
			for _, m := range memories {
				content += fmt.Sprintf("  - %s\n", m.Content)
			}
		}

		// Fetch episode memories involving this user.
		episodes, err := a.memory.SearchByType(ctx, platformUserID, memory.MemoryTypeEpisode, 3)
		if err != nil {
			a.logger.Debug("episode memory search failed", "error", err)
		}
		if len(episodes) > 0 {
			content += "Shared episodes:\n"
			for _, e := range episodes {
				content += fmt.Sprintf("  - %s (%s)\n", e.Content, e.CreatedAt.Format("2006-01-02"))
			}
		}
	}

	// Fetch guild/channel associations.
	guilds, err := a.users.GetUserGuilds(ctx, u.ID)
	if err != nil {
		a.logger.Debug("user guild lookup failed", "error", err)
	}
	if len(guilds) > 0 {
		type guildInfo struct {
			name     string
			channels []string
		}
		guildMap := make(map[string]*guildInfo)
		var guildOrder []string
		for _, g := range guilds {
			gi, ok := guildMap[g.GuildID]
			if !ok {
				gi = &guildInfo{name: g.GuildName}
				guildMap[g.GuildID] = gi
				guildOrder = append(guildOrder, g.GuildID)
			}
			chLabel := g.ChannelName
			if chLabel == "" {
				chLabel = g.ChannelID
			}
			gi.channels = append(gi.channels, chLabel)
		}
		content += "Servers:\n"
		for _, gid := range guildOrder {
			gi := guildMap[gid]
			label := gi.name
			if label == "" {
				label = gid
			}
			content += fmt.Sprintf("  %s: %s\n", label, strings.Join(gi.channels, ", "))
		}
	}

	return content
}

// buildMemoryContext searches long-term memory and returns relevant results
// as a string. Returns "" if no relevant memories found.
// The result is used as an ephemeral message (not persisted in context).
func (a *Agent) buildMemoryContext(ctx context.Context, query string) string {
	memories, err := a.memory.Search(ctx, query, 3)
	if err != nil {
		a.logger.Debug("memory search failed", "error", err)
		return ""
	}
	if len(memories) == 0 {
		return ""
	}

	content := "Relevant memories:\n"
	for _, m := range memories {
		content += fmt.Sprintf("- [%s] %s\n", m.Type, m.Content)
	}
	return content
}

// injectChannelHistory fetches recent messages for a channel not yet seen
// in the context. Uses the discord_get_history tool if available (platform-agnostic),
// falling back to recent memory search.
func (a *Agent) injectChannelHistory(ctx context.Context, channelID, messageContent, source string) {
	if channelID == "" {
		return
	}
	if a.ctx.HasChannelHistory(channelID) {
		return
	}
	// Mark early to avoid retrying on failure.
	a.ctx.MarkChannelSeen(channelID)

	// Try fetching history via the platform's history tool.
	var content string
	if histTool, ok := a.tools.Get("discord_get_history"); ok {
		input, _ := json.Marshal(map[string]any{
			"channel_id": channelID,
			"limit":      10,
		})
		result, err := histTool.Execute(ctx, input)
		if err != nil {
			a.logger.Warn("channel history tool failed", "channel", channelID, "error", err)
		} else if result != nil && !result.IsError && len(result.Content) > 0 && result.Content[0].Text != "" {
			content = a.formatChannelHistory(ctx, channelID, result.Content[0].Text, source)
		}
	}

	// Fallback: search recent memories.
	if content == "" && a.memory != nil {
		since := time.Now().Add(-3 * 24 * time.Hour)
		memories, err := a.memory.SearchRecent(ctx, messageContent, 5, since)
		if err != nil {
			a.logger.Debug("channel memory fallback failed", "error", err)
		}
		if len(memories) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "[Recent related memories for channel=%s]\n", channelID)
			for _, m := range memories {
				fmt.Fprintf(&b, "- [%s] %s\n", m.Type, m.Content)
			}
			content = b.String()
		}
	}

	if content != "" {
		a.ctx.Add(llm.Message{
			Role:      "system",
			Content:   content,
			Timestamp: time.Now(),
		})
		a.logger.Info("channel history injected", "channel", channelID, "length", len(content))
	}
}

// formatChannelHistory parses the history tool's JSON output and resolves
// author names via the user store, producing a human-readable summary.
func (a *Agent) formatChannelHistory(ctx context.Context, channelID, rawJSON, source string) string {
	type histMsg struct {
		AuthorID string `json:"author_id"`
		Author   string `json:"author"`
		Content  string `json:"content"`
		Time     string `json:"time"`
	}
	var msgs []histMsg
	if err := json.Unmarshal([]byte(rawJSON), &msgs); err != nil {
		// Can't parse — return raw JSON wrapped in header.
		return fmt.Sprintf("[Recent history for channel=%s]\n%s", channelID, rawJSON)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[Recent history for channel=%s]\n", channelID)
	for _, m := range msgs {
		name := m.Author
		if m.AuthorID == a.botID {
			// Bot's own messages — label as self without creating a user record.
			name = "suzuha (self)"
		} else if a.users != nil && m.AuthorID != "" {
			if u, err := a.users.Resolve(ctx, source, m.AuthorID, m.Author); err == nil && u.DisplayName != "" {
				name = u.DisplayName
			}
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.Time, name, m.Content)
	}
	return b.String()
}

// stripThinkTags removes LLM thinking/reasoning content from the response text.
// Handles multiple formats used by GLM-4, DeepSeek, etc.:
//   - <think>...</think>  (standard pair)
//   - ...content...</think> (missing opening tag — everything before </think> is thinking)
func stripThinkTags(text string) string {
	// If </think> is present, only keep what comes after the last </think>.
	const closeTag = "</think>"
	if idx := strings.LastIndex(text, closeTag); idx >= 0 {
		text = text[idx+len(closeTag):]
	}
	return strings.TrimSpace(text)
}

// stripDirectiveTags removes [RESPOND], [LISTEN], [SKIP] tags that the LLM
// may echo back in its response.
func stripDirectiveTags(text string) string {
	for _, tag := range []string{"[RESPOND]", "[LISTEN]", "[SKIP]"} {
		text = strings.ReplaceAll(text, tag, "")
	}
	return strings.TrimSpace(text)
}

// responseDirective returns a system instruction telling the LLM whether
// it must respond or may stay silent, based on the event type and affinity axes.
// closeness controls warmth of response; interest controls eagerness to engage.
func responseDirective(evt event.Event, botID string, closeness, interest float64) string {
	if isDirectlyAddressed(evt, botID) {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
	}
	const noEmoji = "※テキストに絵文字・顔文字は絶対に入れないで。"

	const reactHint = "スキップする前に、相手のメッセージに discord_react でリアクションを付けることを検討して" +
		"（テキストに絵文字を書くのではなく、ツールを呼んで相手のメッセージにリアクションを付ける）。" +
		"何も付けなくてもいいけど、共感・面白い・なるほど等の気持ちがあるなら付けてから `[SKIP]`。"

	const skipDefault = "基本は `[SKIP]` してください。あなたが発言しなくても会話は成り立ちます。"

	// interest drives engagement tendency; closeness drives warmth.
	switch {
	case closeness >= 3.0:
		return "[LISTEN] 仲の良い人の会話です。" + skipDefault +
			"本当に一言言いたいときだけ短く返して。相槌だけの返答はしない。" +
			reactHint +
			noEmoji
	case interest >= 2.0:
		return "[LISTEN] 気になる人の会話です。" + skipDefault +
			"自分が詳しい話題や強い意見があるときだけ返して。" +
			reactHint +
			noEmoji
	case closeness <= -1.0:
		return "[LISTEN] チャンネルの会話です。`[SKIP]` とだけ返してください。" +
			noEmoji
	default:
		return "[LISTEN] チャンネルの会話です。" + skipDefault +
			"自分宛の話題か、本当に付け加える価値があるときだけ返して。" +
			reactHint +
			noEmoji
	}
}

// isSilentResponse returns true if the LLM chose not to respond.
func isSilentResponse(text string) bool {
	return text == "" || strings.Contains(strings.ToUpper(text), "[SKIP]")
}

// truncate shortens a string to maxLen runes, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// eventToMessage converts an event to an llm.Message.
func eventToMessage(evt event.Event) llm.Message {
	content, _ := evt.Payload["content"].(string)
	userID, _ := evt.Payload["user_id"].(string)
	userName, _ := evt.Payload["user_name"].(string)
	channel, _ := evt.Payload["channel"].(string)
	channelName, _ := evt.Payload["channel_name"].(string)
	messageID, _ := evt.Payload["message_id"].(string)
	guildID, _ := evt.Payload["guild_id"].(string)
	guildName, _ := evt.Payload["guild_name"].(string)

	return llm.Message{
		Role:        "user",
		Content:     content,
		UserID:      userID,
		UserName:    userName,
		Source:      evt.Source,
		Channel:     channel,
		ChannelName: channelName,
		GuildID:     guildID,
		GuildName:   guildName,
		MessageID:   messageID,
		Timestamp:   evt.Timestamp,
	}
}
