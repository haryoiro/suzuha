package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	db      *sql.DB // shared DB for channel activity tracking
	logger  *slog.Logger
	metrics *observe.Metrics

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
func (a *Agent) Run(ctx context.Context) error {
	events := a.bus.Subscribe()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-events:
			if a.metrics != nil {
				a.metrics.EventsTotal.WithLabelValues(evt.Source, evt.Type).Inc()
			}
			if err := a.handleEvent(ctx, evt); err != nil {
				a.logger.Error("handle event failed", "event_id", evt.ID, "error", err)
			}
		}
	}
}

func (a *Agent) handleEvent(ctx context.Context, evt event.Event) error {
	// 1. Convert event to message.
	msg := eventToMessage(evt)

	a.logger.Info("event received",
		"source", evt.Source, "type", evt.Type,
		"user", msg.UserName, "user_id", msg.UserID,
		"channel", msg.Channel, "content", truncate(msg.Content, 100))

	// 2. Resolve user identity (auto-create if not exists).
	// Skip resolution for the bot's own messages.
	if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
		u, err := a.users.Resolve(ctx, msg.Source, msg.UserID, msg.UserName)
		if err != nil {
			a.logger.Warn("user resolve failed", "error", err)
		} else {
			if u.DisplayName != "" {
				msg.UserName = u.DisplayName
				a.logger.Debug("user resolved", "display_name", u.DisplayName, "role", u.Role, "affinity", u.Affinity)
			}
			// 2.1. Track user's guild/channel association.
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

	// 2.5. Track channel activity for topic backoff (non-bot messages only).
	if msg.Channel != "" && a.db != nil && msg.UserID != a.botID {
		_, _ = a.db.ExecContext(ctx,
			`INSERT INTO channel_activity (channel_id, last_user_message_at) VALUES (?, ?)
			 ON CONFLICT(channel_id) DO UPDATE SET last_user_message_at = excluded.last_user_message_at`,
			msg.Channel, time.Now())
	}

	// 3. Bootstrap channel history if this is a new channel.
	a.injectChannelHistory(ctx, msg.Channel, msg.Content, msg.Source)

	// 4. Add to context.
	a.ctx.Add(msg)

	// 5. Inject user profile if not yet in context (skip for bot itself).
	if a.users != nil && msg.UserID != "" && msg.UserID != a.botID {
		a.injectUserProfile(ctx, msg.Source, msg.UserID)
	}

	// 6. Check context window usage — compact if needed.
	ratio := a.ctx.UsageRatio()
	a.logger.Debug("context window", "usage_ratio", fmt.Sprintf("%.2f", ratio), "message_count", len(a.ctx.Messages()))
	if a.contextWindowPct > 0 && ratio > a.contextWindowPct {
		a.logger.Info("context compaction triggered", "ratio", fmt.Sprintf("%.2f", ratio))
		a.compact(ctx)
	}

	// 7. Retrieve relevant long-term memories.
	if a.memory != nil {
		a.injectMemories(ctx, msg.Content)
	}

	// 8. Determine response directive based on message type.
	directive := responseDirective(evt, a.botID)
	a.logger.Info("llm request", "message_count", len(a.ctx.Messages()), "directive", directive)

	// 9. LLM completion with tool loop.
	// The directive is injected as a transient system message visible to the LLM
	// but not persisted in the conversation history.
	channel, _ := evt.Payload["channel"].(string)
	resp, err := a.completeWithTools(ctx, directive, channel)
	if err != nil {
		return fmt.Errorf("agent: complete: %w", err)
	}

	// 10. Add assistant response to context.
	a.ctx.Add(llm.Message{
		Role:      "assistant",
		Content:   resp.Text,
		Channel:   channel,
		Timestamp: time.Now(),
		ToolCalls: resp.ToolCalls,
	})

	// 11. Send response (strip LLM thinking tags, directive tags, and silent markers).
	text := stripDirectiveTags(stripThinkTags(resp.Text))
	if isSilentResponse(text) {
		a.logger.Info("skipping response (silent)",
			"raw_text", truncate(resp.Text, 100))
	} else {
		a.logger.Info("sending response",
			"channel", channel,
			"length", len(text),
			"content", truncate(text, 200))
		if err := a.chat.Send(ctx, channel, text); err != nil {
			return fmt.Errorf("agent: send: %w", err)
		}
	}

	// 12. Persist context to DB (survives restarts).
	persistContext(ctx, a.db, a.ctx, a.logger)

	return nil
}

// completeWithTools runs the LLM and executes tool calls in a loop.
// The directive is appended as a transient system message at the end of the
// message list for the first LLM call only; it is not persisted in context.
func (a *Agent) completeWithTools(ctx context.Context, directive, channel string) (*llm.Response, error) {
	allTools := a.tools.All()
	maxIter := 10

	for iter := range maxIter {
		msgs := a.ctx.MessagesWithSystem()
		// Inject directive only on the first iteration (before any tool calls).
		if iter == 0 && directive != "" {
			msgs = append(msgs, llm.Message{
				Role:      "system",
				Content:   directive,
				Timestamp: time.Now(),
			})
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

// injectUserProfile loads a user's profile and affinity history into the context
// if it hasn't been injected yet in this context window.
func (a *Agent) injectUserProfile(ctx context.Context, platform, platformUserID string) {
	if a.ctx.HasUserProfile(platformUserID) {
		return
	}

	u, err := a.users.Resolve(ctx, platform, platformUserID, "")
	if err != nil {
		a.logger.Debug("user profile lookup failed", "error", err)
		return
	}

	// Build profile summary.
	var content string
	content = fmt.Sprintf("[User profile: %s (ID=%s) role=%s affinity=%.2f]\n",
		u.DisplayName, u.ID, u.Role, u.Affinity)

	// Fetch recent affinity history.
	events, err := a.users.GetAffinity(ctx, u.ID, 5)
	if err != nil {
		a.logger.Debug("affinity history lookup failed", "error", err)
	}
	if len(events) > 0 {
		content += "Recent affinity history:\n"
		for _, e := range events {
			content += fmt.Sprintf("  %+.1f: %s (%s)\n", e.Delta, e.Reason, e.GroupEnd.Format("2006-01-02"))
		}
	}

	// Fetch user-type memories by user ID.
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

	a.ctx.Add(llm.Message{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	})
	a.ctx.MarkUserProfileInjected(platformUserID)
}

// injectMemories searches long-term memory and injects relevant results.
func (a *Agent) injectMemories(ctx context.Context, query string) {
	memories, err := a.memory.Search(ctx, query, 3)
	if err != nil {
		a.logger.Debug("memory search failed", "error", err)
		return
	}

	if len(memories) == 0 {
		return
	}

	content := "Relevant memories:\n"
	for _, m := range memories {
		content += fmt.Sprintf("- [%s] %s\n", m.Type, m.Content)
	}

	a.ctx.Add(llm.Message{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	})
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
// it must respond or may stay silent, based on the event type.
func responseDirective(evt event.Event, botID string) string {
	if isDirectlyAddressed(evt, botID) {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
	}
	return "[LISTEN] チャンネルの会話です。会話に混ざりたいときは返答し、そうでなければ `[SKIP]` とだけ返してください。"
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
