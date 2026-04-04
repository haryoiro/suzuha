package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

type ChannelProvider struct{}

func (ChannelProvider) ProvideContext(_ context.Context, req Request) Block {
	var block Block

	if req.Source == "discord" && req.Channel != "" {
		if summary := buildOtherChannels(req.Messages, req.Channel); summary != "" {
			block.Background = append(block.Background, llm.Message{
				Role: "system", Content: summary, Timestamp: jtime.Now(),
			})
		}
	}

	if req.IsHome {
		block.Foreground = append(block.Foreground, llm.Message{
			Role: "system", Content: "ここは自分の住処チャンネルです。リラックスして自由に話して。",
			Timestamp: jtime.Now(),
		})
	}

	return block
}

func buildOtherChannels(msgs []llm.Message, currentChannel string) string {
	type chInfo struct {
		name   string
		isDM   bool
		userID string
	}
	channels := make(map[string]*chInfo)

	for _, m := range msgs {
		if m.Channel == "" || m.Channel == currentChannel || m.Role == "system" {
			continue
		}
		if _, ok := channels[m.Channel]; ok {
			info := channels[m.Channel]
			if m.ChannelName != "" {
				info.name = m.ChannelName
			}
			if m.GuildID != "" {
				info.isDM = false
			}
			if info.isDM && m.Role == "user" && m.UserID != "" {
				info.userID = m.UserID
			}
			continue
		}
		channels[m.Channel] = &chInfo{
			name:   m.ChannelName,
			isDM:   m.GuildID == "",
			userID: m.UserID,
		}
	}

	if len(channels) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[他のチャンネル]\n")
	sb.WriteString("discord_get_history で内容を確認できます。\n")
	sb.WriteString("そのチャンネルで発言すると会話コンテキストが切り替わります。\n\n")

	for chID, info := range channels {
		if info.isDM {
			label := info.userID
			if info.name != "" {
				label = info.name
			}
			fmt.Fprintf(&sb, "- DM:%s (user:%s, channel:%s)\n", label, info.userID, chID)
		} else {
			label := info.name
			if label == "" {
				label = chID
			}
			fmt.Fprintf(&sb, "- #%s (channel:%s)\n", label, chID)
		}
	}

	return sb.String()
}
