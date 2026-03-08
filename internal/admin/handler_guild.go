package admin

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

func (h *AdminHandler) GuildsList(ctx context.Context) (*api.GuildsListOK, error) {
	guilds, err := h.userStore.ListGuilds(ctx)
	if err != nil {
		h.logger.Error("ギルド一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Guild, 0, len(guilds))
	for _, g := range guilds {
		data = append(data, api.Guild{
			ID:           g.ID,
			Name:         g.Name,
			UpdatedAt:    g.UpdatedAt.Format("2006-01-02 15:04:05"),
			MemberCount:  int32(g.MemberCount),
			ChannelCount: int32(g.ChannelCount),
		})
	}
	return &api.GuildsListOK{Data: data}, nil
}

func (h *AdminHandler) ChannelsList(ctx context.Context) (*api.ChannelsListOK, error) {
	channels, err := h.userStore.ListAllChannels(ctx)
	if err != nil {
		h.logger.Error("全チャンネル一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Channel, 0, len(channels))
	for _, c := range channels {
		data = append(data, api.Channel{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			GuildID:     c.GuildID,
			GuildName:   c.GuildName,
		})
	}
	return &api.ChannelsListOK{Data: data}, nil
}

func (h *AdminHandler) GuildsChannels(ctx context.Context, params api.GuildsChannelsParams) (*api.GuildsChannelsOK, error) {
	channels, err := h.userStore.GetGuildChannels(ctx, params.ID)
	if err != nil {
		h.logger.Error("ギルドのチャンネル取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.GuildChannel, 0, len(channels))
	for _, c := range channels {
		gc := api.GuildChannel{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			UserCount:   int32(c.UserCount),
			LastSeenAt:  c.LastSeenAt,
		}
		if c.LastUserMessageAt != nil {
			gc.LastUserMessageAt = api.NewOptString(*c.LastUserMessageAt)
		}
		data = append(data, gc)
	}
	return &api.GuildsChannelsOK{Data: data}, nil
}
