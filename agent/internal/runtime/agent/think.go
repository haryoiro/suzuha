package agent

import (
	"context"
	"sync"
	"time"

	domainchannel "github.com/haryoiro/suzuha/internal/domain/channel"
	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/runtime/agent/prompt"
	"github.com/haryoiro/suzuha/internal/runtime/event"
)

const (
	convActiveWindow  = 2 * time.Minute
	convActiveMaxMsgs = 3
	convRecentWindow  = 5 * time.Minute
	convRecentMaxMsgs = 6
	convScanLimit     = 50

	noTimeReport = "※時報禁止（「静かな午後だ」「X時だ」等、時刻・雰囲気の報告をテキストに含めない）。"
)

type convState struct {
	botLastSpokeAgo       time.Duration
	messagesSinceBotSpoke int
	recentDistinctUsers   int
}

func (a *Agent) conversationState(channel string) convState {
	return conversationStateFrom(a.contexts[SourceKeyDiscord].Messages(), channel, a.botID)
}

func conversationStateFrom(msgs []message.Message, channel, botID string) convState {
	now := time.Now()
	cs := convState{botLastSpokeAgo: -1}

	userSet := make(map[string]struct{})
	scanned := 0

	for i := len(msgs) - 1; i >= 0 && scanned < convScanLimit; i-- {
		m := msgs[i]
		if m.Channel != channel {
			continue
		}
		if m.Injected {
			// 注入された過去メッセージは「さっき喋った」判定に含めない。
			continue
		}
		scanned++

		if m.Role == "assistant" && m.UserID == botID {
			if cs.botLastSpokeAgo < 0 {
				cs.botLastSpokeAgo = now.Sub(m.Timestamp)
			}
			continue
		}

		if m.Role == "user" && m.UserID != "" && m.UserID != botID {
			if cs.botLastSpokeAgo < 0 {
				cs.messagesSinceBotSpoke++
			}
			userSet[m.UserID] = struct{}{}
		}
	}

	cs.recentDistinctUsers = len(userSet)
	if cs.botLastSpokeAgo < 0 {
		cs.botLastSpokeAgo = 0
		cs.messagesSinceBotSpoke = convScanLimit
	}
	return cs
}

type episodeSig struct {
	count     int
	hasRecent bool
}

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

func (a *Agent) Think(ctx context.Context, p *Perception) *Thought {
	return a.ThinkWith(ctx, a.contexts[SourceKeyDiscord], p, DirectiveConfig{})
}

func (a *Agent) ThinkWith(ctx context.Context, agentCtx *Context, p *Perception, dc DirectiveConfig) *Thought {
	msgs := agentCtx.Messages()

	// Perception/Context → prompt.Request に変換
	req := prompt.Request{
		Query:        p.LastMessage.Content,
		ImageURLs:    p.LastMessage.ImageURLs,
		Source:       p.LastEvent.Source,
		EventType:    string(p.LastEvent.Type),
		Channel:      p.Channel,
		BotID:        a.botID,
		Messages:     msgs,
		Participants: extractParticipants(msgs, a.botID),
		EventContent: p.LastMessage.Content,
	}
	if a.channelSettings != nil && p.Channel != "" {
		req.IsHome = a.channelSettings.Get(p.Channel).Home
	}

	blocks := a.collectContext(ctx, req)

	var bg, fg []message.Message
	for _, b := range blocks {
		bg = append(bg, b.Background...)
		fg = append(fg, b.Foreground...)
	}

	if !dc.ForceRespond && a.channelSettings != nil && p.Channel != "" && !p.IsDM {
		if a.channelSettings.GetMode(p.Channel) == domainchannel.ModeListen {
			a.logger.Info("聞いてるだけ", "channel", p.Channel)
			return &Thought{ListenMode: true}
		}
	}

	directive := a.resolveDirective(agentCtx, p, dc)

	// 記憶検索結果が空の場合、捏造防止の注意を directive に追加
	if len(bg) == 0 && !dc.SkipChannelHistory {
		directive += "\n※この話題に関連する記憶が見つかっていません。覚えていないことは正直に「覚えてない」「知らない」と答えてください。知ったかぶりや捏造は禁止。"
	}

	a.logger.Info("考え中",
		"message_count", len(agentCtx.Messages()),
		"background_count", len(bg),
		"foreground_count", len(fg),
		"directive", directive)

	// admin 表示用キャッシュ
	a.lastEphemeralMu.Lock()
	a.lastBackground = bg
	a.lastForeground = fg
	a.lastEphemeralMu.Unlock()

	return &Thought{
		Background: bg,
		Foreground: fg,
		Directive:  directive,
	}
}

func (a *Agent) collectContext(ctx context.Context, req prompt.Request) []prompt.Block {
	blocks := make([]prompt.Block, len(a.contextProviders))
	var wg sync.WaitGroup
	for i, p := range a.contextProviders {
		wg.Add(1)
		go func(i int, p prompt.Provider) {
			defer wg.Done()
			blocks[i] = p.ProvideContext(ctx, req)
		}(i, p)
	}
	wg.Wait()
	return blocks
}

func extractParticipants(msgs []message.Message, botID string) []prompt.Participant {
	seen := make(map[string]bool)
	var out []prompt.Participant
	count := 0
	for i := len(msgs) - 1; i >= 0 && count < 10; i-- {
		m := msgs[i]
		if m.UserID == "" || m.UserID == botID || m.Role != "user" {
			continue
		}
		if m.Injected {
			// 注入された過去発言者は「現参加者」リストに入れない。
			continue
		}
		count++
		if !seen[m.UserID] {
			seen[m.UserID] = true
			out = append(out, prompt.Participant{Platform: m.Source, UserID: m.UserID})
		}
	}
	return out
}

func (a *Agent) resolveDirective(agentCtx *Context, p *Perception, dc DirectiveConfig) string {
	if dc.DirectiveTemplate != "" {
		return dc.DirectiveTemplate
	}
	if p.LastEvent.Source == "device" {
		return "[RESPOND] 物理デバイス経由の音声対話です。必ず返答してください。" +
			"話し言葉で自然に返して。1〜2文で短く。" +
			"skip_response は使わないで。" +
			"※テキストに絵文字・顔文字は入れない。音声で読まれるので句読点や記号は控えめに。"
	}
	if p.LastEvent.Type == event.TypeSelfPrompt {
		return "[SELF_PROMPT] 自分の内なる思考。\n" +
			"気になったことを調べたり、誰かに声をかけたり、音楽を変えたり、自由にやっていい。\n" +
			"使えるツールは全部使っていい。目的のない行動はしない。\n" +
			"※時刻や雰囲気の報告はしない（「静かな午後だ」「X時だ」等）。"
	}
	if p.DirectlyAddressed {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。※返答は1〜2行に収めて。長文禁止。" + noTimeReport
	}

	cs := conversationStateFrom(agentCtx.Messages(), p.Channel, a.botID)
	es := a.episodeSignal(context.Background(), p.LastMessage.Source, p.LastMessage.UserID)
	return responseDirective(p.LastEvent, a.botID, cs, es)
}

func responseDirective(evt event.Event, botID string, cs convState, es episodeSig) string {
	if isDirectlyAddressed(evt, botID) {
		return "[RESPOND] あなた宛のメッセージです。必ず返答してください。※返答は1〜2行に収めて。長文禁止。" + noTimeReport
	}
	const noEmoji = "※テキストに絵文字・顔文字は絶対に入れないで。"
	const brevity = "※返答は1〜2行に収めて。長文禁止。"
	const reactHint = "心が動いたときだけ discord_react でリアクションしてよい。"
	const skipDefault = "基本は skip_response ツールを呼んでスキップしてください。あなたが発言しなくても会話は成り立ちます。"

	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convActiveWindow && cs.messagesSinceBotSpoke <= convActiveMaxMsgs {
		return "[RESPOND] 直前まであなたが参加していた会話の続きです。返答してください。" + brevity + noEmoji + noTimeReport
	}

	if cs.botLastSpokeAgo > 0 && cs.botLastSpokeAgo < convRecentWindow && cs.messagesSinceBotSpoke <= convRecentMaxMsgs && cs.recentDistinctUsers == 1 {
		return "[LISTEN] 最近この会話に参加していました。続ける価値があれば短く返してください。なければ skip_response を呼んで。" +
			brevity + reactHint + noEmoji
	}

	if es.count >= 3 && es.hasRecent {
		return "[LISTEN] 仲の良い人の会話です。気軽に返して。相槌だけの返答はしない。話すことがなければ skip_response。" +
			brevity + noEmoji
	}
	if es.count >= 1 {
		return "[LISTEN] 知り合いの会話です。" + skipDefault +
			"自分が詳しい話題や強い意見があるときだけ返して。" +
			brevity + reactHint + noEmoji
	}

	return "[LISTEN] チャンネルの会話です。" + skipDefault +
		"自分宛の話題か、本当に付け加える価値があるときだけ返して。" +
		brevity + reactHint + noEmoji
}
