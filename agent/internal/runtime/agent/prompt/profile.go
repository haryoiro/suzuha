package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	portmem "github.com/haryoiro/suzuha/internal/port/memory"
	"github.com/haryoiro/suzuha/internal/port/user"
)

// ProfileProvider は参加者のプロフィール情報をプロンプトブロックとして提供する。
type ProfileProvider struct {
	Users  user.Store
	Memory portmem.Memory
	BotID  string
	Logger *slog.Logger
}

func (p *ProfileProvider) ProvideContext(ctx context.Context, req Request) Block {
	if p.Users == nil {
		return Block{}
	}

	type indexedMsg struct {
		index   int
		content string
	}
	results := make([]indexedMsg, 0, len(req.Participants)+1)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, pt := range req.Participants {
		wg.Add(1)
		go func(idx int, platform, userID string) {
			defer wg.Done()
			content := p.buildProfile(ctx, platform, userID)
			if content != "" {
				mu.Lock()
				results = append(results, indexedMsg{idx, content})
				mu.Unlock()
			}
		}(i, pt.Platform, pt.UserID)
	}

	if p.Memory != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			selfMems, err := p.Memory.ListByType(ctx, memo.MemoryTypeSelf, 3)
			if err == nil && len(selfMems) > 0 {
				var sb strings.Builder
				sb.WriteString("[自己認識]\n")
				for _, m := range selfMems {
					fmt.Fprintf(&sb, "  - %s\n", m.Content)
				}
				mu.Lock()
				results = append(results, indexedMsg{len(req.Participants), sb.String()})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	slices.SortFunc(results, func(a, b indexedMsg) int { return a.index - b.index })

	out := make([]message.Message, 0, len(results))
	for _, r := range results {
		out = append(out, message.Message{Role: "system", Content: r.content, Timestamp: jtime.Now()})
	}
	return Block{Background: out}
}

func (p *ProfileProvider) buildProfile(ctx context.Context, platform, platformUserID string) string {
	u, err := p.Users.Resolve(ctx, platform, platformUserID, "")
	if err != nil {
		p.Logger.Debug("相手のことを思い出せなかった", "error", err)
		return ""
	}

	// Header は本文メタデータと揃えた key=value 形式。
	content := fmt.Sprintf("[プロフィール platform=%s user_id=%s user=%s 役割=%s]\n",
		platform, platformUserID, u.DisplayName, u.Role)

	if p.Memory != nil {
		memories, err := p.Memory.ListByUser(ctx, u.ID, 5)
		if err != nil {
			p.Logger.Debug("相手との記憶を探せなかった", "error", err)
		}
		if len(memories) > 0 {
			content += "知ってること:\n"
			for _, m := range memories {
				content += fmt.Sprintf("  - %s\n", m.Content)
			}
		}

		episodes, err := p.Memory.ListEpisodesByParticipant(ctx, platformUserID, 3)
		if err != nil {
			p.Logger.Debug("エピソードの記憶を探せなかった", "error", err)
		}
		if len(episodes) > 0 {
			content += "共有エピソード:\n"
			for _, e := range episodes {
				content += fmt.Sprintf("  - %s (%s)\n", e.Content, e.CreatedAt.Format("2006-01-02"))
			}
		}
	}

	guilds, err := p.Users.GetUserGuilds(ctx, u.ID)
	if err != nil {
		p.Logger.Debug("サーバー情報の取得に失敗", "error", err)
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
		content += "参加場所:\n"
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
