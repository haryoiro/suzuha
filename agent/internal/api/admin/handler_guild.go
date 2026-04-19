package admin

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

func (h *AdminHandler) GuildsList(ctx context.Context) (*gen.GuildsListOK, error) {
	guilds, err := h.userStore.ListGuilds(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.Guild, 0, len(guilds))
	for _, g := range guilds {
		data = append(data, gen.Guild{
			ID:           g.ID,
			Name:         g.Name,
			UpdatedAt:    g.UpdatedAt.Format("2006-01-02 15:04:05"),
			MemberCount:  int32(g.MemberCount),
			ChannelCount: int32(g.ChannelCount),
		})
	}
	return &gen.GuildsListOK{Data: data}, nil
}

func (h *AdminHandler) ChannelsList(ctx context.Context) (*gen.ChannelsListOK, error) {
	channels, err := h.userStore.ListAllChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.Channel, 0, len(channels))
	for _, c := range channels {
		data = append(data, gen.Channel{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			GuildID:     c.GuildID,
			GuildName:   c.GuildName,
		})
	}
	return &gen.ChannelsListOK{Data: data}, nil
}

func (h *AdminHandler) GuildsChannels(ctx context.Context, params gen.GuildsChannelsParams) (*gen.GuildsChannelsOK, error) {
	channels, err := h.userStore.GetGuildChannels(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.GuildChannel, 0, len(channels))
	for _, c := range channels {
		gc := gen.GuildChannel{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			UserCount:   int32(c.UserCount),
			LastSeenAt:  c.LastSeenAt,
		}
		if c.LastUserMessageAt != nil {
			gc.LastUserMessageAt = gen.NewOptString(*c.LastUserMessageAt)
		}
		data = append(data, gc)
	}
	return &gen.GuildsChannelsOK{Data: data}, nil
}
