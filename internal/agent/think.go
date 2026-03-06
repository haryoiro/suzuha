package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// Conversation-state thresholds — tunables for directive priority 2 & 3.
const (
	convActiveWindow   = 2 * time.Minute // priority 2: bot spoke within this window
	convActiveMaxMsgs  = 3               // priority 2: max user messages since bot spoke
	convRecentWindow   = 5 * time.Minute // priority 3: bot spoke within this window
	convRecentMaxMsgs  = 6               // priority 3: max user messages since bot spoke
	convScanLimit      = 50              // max messages to scan backwards
)

// convState captures dynamic conversation signals derived from the message history.
type convState struct {
	botLastSpokeAgo       time.Duration // time since bot's last message in this channel
	messagesSinceBotSpoke int           // user messages after the bot's last message
	recentDistinctUsers   int           // distinct non-bot users in recent messages
}

// conversationState scans the agent's context messages backwards to compute
// conversation-state signals for the given channel.
func (a *Agent) conversationState(channel string) convState {
	msgs := a.ctx.Messages()
	now := time.Now()
	cs := convState{
		botLastSpokeAgo: -1, // sentinel: bot never spoke
	}

	userSet := make(map[string]struct{})
	scanned := 0

	for i := len(msgs) - 1; i >= 0 && scanned < convScanLimit; i-- {
		m := msgs[i]
		// Only consider messages in the same channel.
		if m.Channel != channel {
			continue
		}
		scanned++

		if m.Role == "assistant" && m.UserID == a.botID {
			if cs.botLastSpokeAgo < 0 {
				// First (most recent) bot message found.
				cs.botLastSpokeAgo = now.Sub(m.Timestamp)
			}
			continue
		}

		if m.Role == "user" && m.UserID != "" && m.UserID != a.botID {
			// Count messages before we've found the bot's last message.
			if cs.botLastSpokeAgo < 0 {
				cs.messagesSinceBotSpoke++
			}
			userSet[m.UserID] = struct{}{}
		}
	}

	cs.recentDistinctUsers = len(userSet)
	if cs.botLastSpokeAgo < 0 {
		cs.botLastSpokeAgo = 0 // normalize: never spoke → 0 (handled by large messagesSince)
		cs.messagesSinceBotSpoke = convScanLimit // ensure no active-conversation match
	}
	return cs
}

// Think builds ephemeral context (memories, profiles) and determines
// the response directive. Returns a Thought describing what to do.
func (a *Agent) Think(ctx context.Context, p *Perception) *Thought {
	// Build ephemeral context in parallel.
	var (
		memMsg   string
		locMsg   string
		profiles []llm.Message
		wg       sync.WaitGroup
	)
	if a.memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			memMsg = a.buildMemoryContext(ctx, p.LastMessage.Content)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		profiles = a.buildUserProfiles(ctx)
	}()
	if a.locationStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			locMsg = a.locationStore.BuildContextSnippet()
		}()
	}
	wg.Wait()

	var ephemeral []llm.Message
	if memMsg != "" {
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: memMsg, Timestamp: time.Now(),
		})
	}
	if locMsg != "" {
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: locMsg, Timestamp: time.Now(),
		})
	}
	if len(profiles) > 0 {
		ephemeral = append(ephemeral, profiles...)
	}

	// Check listen mode.
	if a.channelSettings != nil && p.Channel != "" && !p.IsDM {
		mode := a.channelSettings.GetMode(p.Channel)
		if mode == channelpkg.ModeListen {
			a.logger.Info("リッスンモード: 応答せずに取り込み", "channel", p.Channel)
			return &Thought{ListenMode: true}
		}
		if a.channelSettings.Get(p.Channel).Home {
			ephemeral = append(ephemeral, llm.Message{
				Role:      "system",
				Content:   "ここは自分の住処チャンネルです。リラックスして自由に話して。",
				Timestamp: time.Now(),
			})
		}
	}

	// Determine response directive.
	var directive string
	if p.LastEvent.Type == event.TypeSelfPrompt {
		// Self-prompt content is ephemeral — not persisted in main context.
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: p.LastMessage.Content, Timestamp: time.Now(),
		})
		directive = "[SELF_PROMPT] 自分の内なる思考です。使えるツールを自由に組み合わせて暇つぶしして。ステータス変更、ネット散歩、つぶやき、何もしない、なんでもOK。"
	} else if p.DirectlyAddressed {
		directive = "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
	} else {
		cs := a.conversationState(p.Channel)
		directive = responseDirective(p.LastEvent, a.botID, p.MaxCloseness, p.MaxInterest, cs)
	}
	a.logger.Info("LLMリクエスト", "message_count", len(a.ctx.Messages()),
		"ephemeral_count", len(ephemeral), "directive", directive)

	// Cache ephemeral messages for admin visibility.
	a.lastEphemeralMu.Lock()
	a.lastEphemeral = make([]llm.Message, len(ephemeral))
	copy(a.lastEphemeral, ephemeral)
	a.lastEphemeralMu.Unlock()

	return &Thought{
		Ephemeral: ephemeral,
		Directive: directive,
	}
}

// buildMemoryContext searches long-term memory and returns relevant results
// as a string. Returns "" if no relevant memories found.
func (a *Agent) buildMemoryContext(ctx context.Context, query string) string {
	memories, err := a.memory.Search(ctx, query, 3)
	if err != nil {
		a.logger.Debug("メモリ検索失敗", "error", err)
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

// buildUserProfiles collects ephemeral profile messages for all users
// seen in the current context who haven't been profiled yet.
// recentUserLimit controls how many recent user messages to scan backwards
// to determine which users get profile injection.
const recentUserLimit = 10

func (a *Agent) buildUserProfiles(ctx context.Context) []llm.Message {
	if a.users == nil {
		return nil
	}

	type userKey struct{ platform, userID string }

	// Only collect users from the last N user messages.
	msgs := a.ctx.Messages()
	seen := make(map[userKey]bool)
	var keys []userKey
	count := 0
	for i := len(msgs) - 1; i >= 0 && count < recentUserLimit; i-- {
		m := msgs[i]
		if m.UserID == "" || m.UserID == a.botID || m.Role != "user" {
			continue
		}
		count++
		k := userKey{m.Source, m.UserID}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

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

	if a.memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			selfMems, err := a.memory.ListByType(ctx, memory.MemoryTypeSelf, 3)
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

	slices.SortFunc(results, func(a, b indexedMsg) int { return a.index - b.index })

	out := make([]llm.Message, 0, len(results))
	for _, r := range results {
		out = append(out, llm.Message{
			Role: "system", Content: r.content, Timestamp: time.Now(),
		})
	}
	return out
}

// buildUserProfile builds a profile summary string for a single user.
func (a *Agent) buildUserProfile(ctx context.Context, platform, platformUserID string) string {
	u, err := a.users.Resolve(ctx, platform, platformUserID, "")
	if err != nil {
		a.logger.Debug("ユーザープロファイル取得失敗", "error", err)
		return ""
	}

	content := fmt.Sprintf("[User profile: %s (ID=%s) role=%s closeness=%.2f trust=%.2f interest=%.2f]\n",
		u.DisplayName, u.ID, u.Role, u.Closeness, u.Trust, u.Interest)

	events, err := a.users.GetAffinity(ctx, u.ID, 5)
	if err != nil {
		a.logger.Debug("親密度履歴の取得失敗", "error", err)
	}
	if len(events) > 0 {
		content += "Recent affinity history:\n"
		for _, e := range events {
			content += fmt.Sprintf("  %+.1f (%s): %s (%s)\n", e.Delta, e.Axis, e.Reason, e.GroupEnd.Format("2006-01-02"))
		}
	}

	if a.memory != nil {
		memories, err := a.memory.ListByUser(ctx, u.ID, 5)
		if err != nil {
			a.logger.Debug("ユーザーメモリ検索失敗", "error", err)
		}
		if len(memories) > 0 {
			content += "Known facts:\n"
			for _, m := range memories {
				content += fmt.Sprintf("  - %s\n", m.Content)
			}
		}

		episodes, err := a.memory.ListEpisodesByParticipant(ctx, platformUserID, 3)
		if err != nil {
			a.logger.Debug("エピソードメモリ検索失敗", "error", err)
		}
		if len(episodes) > 0 {
			content += "Shared episodes:\n"
			for _, e := range episodes {
				content += fmt.Sprintf("  - %s (%s)\n", e.Content, e.CreatedAt.Format("2006-01-02"))
			}
		}
	}

	guilds, err := a.users.GetUserGuilds(ctx, u.ID)
	if err != nil {
		a.logger.Debug("ユーザーギルド取得失敗", "error", err)
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

// responseDirective returns a system instruction telling the LLM whether
// it must respond or may stay silent.
func responseDirective(evt event.Event, botID string, closeness, interest float64, cs convState) string {
	if isDirectlyAddressed(evt, botID) {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。"
	}
	const noEmoji = "※テキストに絵文字・顔文字は絶対に入れないで。"

	const reactHint = "スキップする前に、相手のメッセージに discord_react でリアクションを付けることを検討して" +
		"（テキストに絵文字を書くのではなく、ツールを呼んで相手のメッセージにリアクションを付ける）。" +
		"何も付けなくてもいいけど、共感・面白い・なるほど等の気持ちがあるなら付けてから skip_response を呼んで。"

	const skipDefault = "基本は skip_response ツールを呼んでスキップしてください。あなたが発言しなくても会話は成り立ちます。"

	// Priority 2: Bot was actively speaking in this conversation very recently.
	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convActiveWindow && cs.messagesSinceBotSpoke <= convActiveMaxMsgs {
		return "[RESPOND] 直前まであなたが参加していた会話の続きです。返答してください。" + noEmoji
	}

	// Priority 3: Bot spoke recently and it's a 1-on-1 thread.
	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convRecentWindow && cs.messagesSinceBotSpoke <= convRecentMaxMsgs && cs.recentDistinctUsers == 1 {
		return "[LISTEN] 最近この会話に参加していました。続ける価値があれば短く返してください。なければ skip_response を呼んで。" +
			reactHint + noEmoji
	}

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
		return "[LISTEN] チャンネルの会話です。skip_response ツールを呼んでスキップしてください。" +
			noEmoji
	default:
		return "[LISTEN] チャンネルの会話です。" + skipDefault +
			"自分宛の話題か、本当に付け加える価値があるときだけ返して。" +
			reactHint +
			noEmoji
	}
}
