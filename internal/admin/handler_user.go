package admin

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/user"
)

func userToAPI(u user.User, links []user.PlatformLink) api.User {
	platforms := make([]api.Platform, 0, len(links))
	for _, l := range links {
		platforms = append(platforms, api.Platform{
			Platform:       l.Platform,
			PlatformUserID: l.PlatformUserID,
			PlatformName:   l.PlatformName,
		})
	}

	return api.User{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		Role:        api.UserRole(u.Role),
		IsBot:       u.IsBot,
		CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:   u.UpdatedAt.Format("2006-01-02 15:04:05"),
		Platforms:   platforms,
	}
}

func (h *AdminHandler) UsersList(ctx context.Context, params api.UsersListParams) (*api.UsersListOK, error) {
	offset := int(params.Offset.Or(0))
	limit := int(params.Limit.Or(50))

	users, total, err := h.userStore.List(ctx, offset, limit)
	if err != nil {
		h.logger.Error("ユーザー一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.User, 0, len(users))
	for _, u := range users {
		links, _ := h.userStore.ListPlatformLinks(ctx, u.ID)
		data = append(data, userToAPI(u, links))
	}
	return &api.UsersListOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) UsersGet(ctx context.Context, params api.UsersGetParams) (*api.UsersGetOK, error) {
	u, err := h.userStore.Get(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("not found")
	}
	links, _ := h.userStore.ListPlatformLinks(ctx, u.ID)
	return &api.UsersGetOK{Data: userToAPI(*u, links)}, nil
}

func (h *AdminHandler) UsersUpdate(ctx context.Context, req *api.UpdateUserRequest, params api.UsersUpdateParams) (*api.OkResponse, error) {
	fields := user.UpdateFields{}

	if v, ok := req.DisplayName.Get(); ok {
		fields.DisplayName = &v
	}
	if v, ok := req.IsBot.Get(); ok {
		fields.IsBot = &v
	}
	if v, ok := req.Role.Get(); ok {
		role := user.Role(v)
		fields.Role = &role
	}

	if err := h.userStore.Update(ctx, params.ID, fields); err != nil {
		h.logger.Error("ユーザーの更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) UsersGuilds(ctx context.Context, params api.UsersGuildsParams) (*api.UsersGuildsOK, error) {
	guilds, err := h.userStore.GetUserGuilds(ctx, params.ID)
	if err != nil {
		h.logger.Error("ユーザーのギルド取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.UserGuild, 0, len(guilds))
	for _, g := range guilds {
		data = append(data, api.UserGuild{
			GuildID:     g.GuildID,
			GuildName:   g.GuildName,
			ChannelID:   g.ChannelID,
			ChannelName: g.ChannelName,
			LastSeenAt:  g.LastSeenAt.Format("2006-01-02 15:04:05"),
		})
	}
	return &api.UsersGuildsOK{Data: data}, nil
}

func (h *AdminHandler) UsersMemories(ctx context.Context, params api.UsersMemoriesParams) (*api.UsersMemoriesOK, error) {
	limit := int(params.Limit.Or(20))
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, content, created_at, updated_at FROM memories
		 WHERE type = 'user' AND json_extract(metadata, '$.user_id') = ?
		 ORDER BY updated_at DESC LIMIT ?`, params.ID, limit)
	if err != nil {
		h.logger.Error("ユーザーのメモリ取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	var data []api.UserMemory
	for rows.Next() {
		var e api.UserMemory
		if err := rows.Scan(&e.ID, &e.Content, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		data = append(data, e)
	}
	if data == nil {
		data = []api.UserMemory{}
	}
	return &api.UsersMemoriesOK{Data: data}, nil
}
