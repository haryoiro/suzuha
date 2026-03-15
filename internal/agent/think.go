package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	channelpkg "github.com/haryoiro/suzuha/internal/channel"
	"github.com/haryoiro/suzuha/internal/embedding"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

func base64encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// modalityFromMime returns the embedding Modality for a MIME type.
func modalityFromMime(mime string) embedding.Modality {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return embedding.ModalityImage
	case strings.HasPrefix(mime, "audio/"):
		return embedding.ModalityAudio
	default:
		return embedding.ModalityText
	}
}

// parseDataURI extracts binary data and MIME type from a data URI.
// Returns (nil, "") if the URI is not a valid data URI.
func parseDataURI(uri string) ([]byte, string) {
	// data:image/png;base64,iVBOR...
	if !strings.HasPrefix(uri, "data:") {
		return nil, ""
	}
	commaIdx := strings.Index(uri, ",")
	if commaIdx < 0 {
		return nil, ""
	}
	header := uri[5:commaIdx] // "image/png;base64"
	mime := strings.TrimSuffix(header, ";base64")
	data, err := base64.StdEncoding.DecodeString(uri[commaIdx+1:])
	if err != nil {
		return nil, ""
	}
	return data, mime
}

// Conversation-state thresholds — tunables for directive priority 2 & 3.
const (
	convActiveWindow   = 2 * time.Minute // priority 2: bot spoke within this window
	convActiveMaxMsgs  = 3               // priority 2: max user messages since bot spoke
	convRecentWindow   = 5 * time.Minute // priority 3: bot spoke within this window
	convRecentMaxMsgs  = 6               // priority 3: max user messages since bot spoke
	convScanLimit      = 50              // max messages to scan backwards

	// noTimeReport is appended to all directives.
	noTimeReport = "※時報禁止（「静かな午後だ」「X時だ」等、時刻・雰囲気の報告をテキストに含めない）。"
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
		memMsgs  []llm.Message
		locMsg   string
		profiles []llm.Message
		wg       sync.WaitGroup
	)
	if a.memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			memMsgs = a.buildMemoryContext(ctx, p.LastMessage.Content, p.LastMessage.ImageURLs)
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
	if len(memMsgs) > 0 {
		ephemeral = append(ephemeral, memMsgs...)
	}
	if locMsg != "" {
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: locMsg, Timestamp: jtime.Now(),
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
				Timestamp: jtime.Now(),
			})
		}
	}

	// Determine response directive.
	var directive string
	if p.LastEvent.Type == event.TypeSelfPrompt {
		// Self-prompt content is ephemeral — not persisted in main context.
		ephemeral = append(ephemeral, llm.Message{
			Role: "system", Content: p.LastMessage.Content, Timestamp: jtime.Now(),
		})
		toolNames := strings.Join(a.tools.AllEnabledNames(), ", ")
		directive = fmt.Sprintf("[SELF_PROMPT] 自分の内なる思考です。以下のツールを自由に組み合わせて遊んでください: %s\n気になることを調べる、誰かが言っていたことを思い出してリマインドする、会話の流れから何か提案する、ステータスを変える、つぶやく、なんでもOK。\n※時報禁止（「静かな午後だ」「X時だ」等、時刻・雰囲気の報告をテキストに含めない）。", toolNames)
	} else if p.DirectlyAddressed {
		directive = "[RESPOND] あなた宛のメッセージです。必ず返答してください。※返答は1〜2行に収めて。長文禁止。" + "※時報禁止（「静かな午後だ」「X時だ」等、時刻・雰囲気の報告をテキストに含めない）。"
	} else {
		cs := a.conversationState(p.Channel)
		es := a.episodeSignal(ctx, p.LastMessage.Source, p.LastMessage.UserID)
		directive = responseDirective(p.LastEvent, a.botID, cs, es)
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
// as LLM messages. If imageURLs are provided (data URIs from Discord),
// also performs multimodal vector search. Attachments (images) are loaded
// from MediaStore and included as data URIs for vision-capable LLMs.
func (a *Agent) buildMemoryContext(ctx context.Context, query string, imageURLs []string) []llm.Message {
	memories, err := a.memory.Search(ctx, query, 5)
	if err != nil {
		a.logger.Debug("メモリ検索失敗", "error", err)
	}

	// If media (images/audio) are present, also search by media embedding.
	if len(imageURLs) > 0 {
		for _, dataURI := range imageURLs {
			data, mime := parseDataURI(dataURI)
			if data == nil {
				continue
			}
			modality := modalityFromMime(mime)
			parts := []embedding.Part{{
				Modality: modality,
				Data:     data,
				MimeType: mime,
			}}
			mediaResults, err := a.memory.SearchByParts(ctx, parts, 5)
			if err != nil {
				a.logger.Debug("メディアメモリ検索失敗", "error", err)
				continue
			}
			// Deduplicate with text results.
			seen := make(map[string]bool, len(memories))
			for _, m := range memories {
				seen[m.ID] = true
			}
			for _, m := range mediaResults {
				if !seen[m.ID] {
					memories = append(memories, m)
					seen[m.ID] = true
				}
			}
		}
	}

	if len(memories) == 0 {
		return nil
	}

	// Build text summary of all memories with attribution.
	var textParts []string
	for _, m := range memories {
		label := string(m.Type)
		if m.Metadata != nil {
			if uid, ok := m.Metadata["user_id"].(string); ok && uid != "" {
				label += " user_id=" + uid
			}
			switch v := m.Metadata["participants"].(type) {
			case []any:
				var ids []string
				for _, p := range v {
					if s, ok := p.(string); ok {
						ids = append(ids, s)
					}
				}
				if len(ids) > 0 {
					label += " participants=" + strings.Join(ids, ",")
				}
			case []string:
				if len(v) > 0 {
					label += " participants=" + strings.Join(v, ",")
				}
			}
			if tone, ok := m.Metadata["emotional_tone"].(string); ok && tone != "" {
				label += " tone=" + tone
			}
		}
		textParts = append(textParts, fmt.Sprintf("- [%s] %s (%s)", label, m.Content, m.CreatedAt.Format("2006-01-02")))
	}
	textContent := "Relevant memories:\n" + strings.Join(textParts, "\n")

	// Collect image data URIs from attachments.
	var attachedImages []string
	if a.mediaStore != nil {
		for _, m := range memories {
			for _, att := range m.Attachments {
				if att.Modality != "image" {
					continue
				}
				data, err := a.mediaStore.Get(ctx, att.Key)
				if err != nil {
					a.logger.Debug("メモリ添付画像の読み込み失敗", "key", att.Key, "error", err)
					continue
				}
				dataURI := fmt.Sprintf("data:%s;base64,%s",
					att.MimeType, base64encode(data))
				attachedImages = append(attachedImages, dataURI)
			}
		}
	}

	if len(attachedImages) > 0 {
		// Use "user" role so vision-capable LLMs process the images.
		return []llm.Message{{
			Role:      "user",
			Content:   "[Memory context with images]\n" + textContent,
			ImageURLs: attachedImages,
			Timestamp: jtime.Now(),
		}}
	}

	return []llm.Message{{
		Role:      "system",
		Content:   textContent,
		Timestamp: jtime.Now(),
	}}
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
			Role: "system", Content: r.content, Timestamp: jtime.Now(),
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

	content := fmt.Sprintf("[User profile: %s (ID=%s) role=%s]\n",
		u.DisplayName, u.ID, u.Role)

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

// episodeSig summarises the episode-based relationship with a user.
type episodeSig struct {
	count     int  // total shared episodes
	hasRecent bool // at least one episode within the last 7 days
}

// episodeSignal queries shared episodes for a user and returns a summary.
func (a *Agent) episodeSignal(ctx context.Context, platform, platformUserID string) episodeSig {
	if a.memory == nil || platformUserID == "" || platformUserID == a.botID {
		return episodeSig{}
	}
	episodes, err := a.memory.ListEpisodesByParticipant(ctx, platformUserID, 5)
	if err != nil {
		return episodeSig{}
	}
	sig := episodeSig{count: len(episodes)}
	if len(episodes) > 0 {
		sig.hasRecent = time.Since(episodes[0].UpdatedAt) < 7*24*time.Hour
	}
	return sig
}

// responseDirective returns a system instruction telling the LLM whether
// it must respond or may stay silent.
func responseDirective(evt event.Event, botID string, cs convState, es episodeSig) string {
	if isDirectlyAddressed(evt, botID) {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。※返答は1〜2行に収めて。長文禁止。" + noTimeReport
	}
	const noEmoji = "※テキストに絵文字・顔文字は絶対に入れないで。"

	const brevity = "※返答は1〜2行に収めて。長文禁止。"

	const reactHint = "リアクションは本当に心が動いたときだけ discord_react で付けてよい。ほとんどの場合はリアクションなしで skip_response だけ呼べばOK。"

	const skipDefault = "基本は skip_response ツールを呼んでスキップしてください。あなたが発言しなくても会話は成り立ちます。"

	// Priority 2: Bot was actively speaking in this conversation very recently.
	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convActiveWindow && cs.messagesSinceBotSpoke <= convActiveMaxMsgs {
		return "[RESPOND] 直前まであなたが参加していた会話の続きです。返答してください。" + brevity + noEmoji + noTimeReport
	}

	// Priority 3: Bot spoke recently and it's a 1-on-1 thread.
	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convRecentWindow && cs.messagesSinceBotSpoke <= convRecentMaxMsgs && cs.recentDistinctUsers == 1 {
		return "[LISTEN] 最近この会話に参加していました。続ける価値があれば短く返してください。なければ skip_response を呼んで。" +
			brevity + reactHint + noEmoji + noTimeReport
	}

	// Episode-based relationship: many shared episodes with recent activity → close relationship.
	if es.count >= 3 && es.hasRecent {
		return "[LISTEN] 仲の良い人の会話です。気軽に返して。相槌だけの返答はしない。話すことがなければ skip_response。" +
			brevity + noEmoji + noTimeReport
	}
	if es.count >= 1 {
		return "[LISTEN] 知り合いの会話です。" + skipDefault +
			"自分が詳しい話題や強い意見があるときだけ返して。" +
			brevity + reactHint + noEmoji + noTimeReport
	}

	return "[LISTEN] チャンネルの会話です。" + skipDefault +
		"自分宛の話題か、本当に付け加える価値があるときだけ返して。" +
		brevity + reactHint + noEmoji + noTimeReport
}
