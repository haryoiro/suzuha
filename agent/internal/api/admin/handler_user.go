package admin

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
	user "github.com/haryoiro/suzuha/internal/domain/user"
)

func userToAPI(u user.User, links []user.PlatformLink) gen.User {
	platforms := make([]gen.Platform, 0, len(links))
	for _, l := range links {
		platforms = append(platforms, gen.Platform{
			Platform:       l.Platform,
			PlatformUserID: l.PlatformUserID,
			PlatformName:   l.PlatformName,
		})
	}

	return gen.User{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		Role:        gen.UserRole(u.Role),
		IsBot:       u.IsBot,
		CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:   u.UpdatedAt.Format("2006-01-02 15:04:05"),
		Platforms:   platforms,
	}
}

func (h *AdminHandler) UsersList(ctx context.Context, params gen.UsersListParams) (*gen.UsersListOK, error) {
	offset := int(params.Offset.Or(0))
	limit := int(params.Limit.Or(50))

	users, total, err := h.userStore.List(ctx, offset, limit)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.User, 0, len(users))
	for _, u := range users {
		links, err := h.userStore.ListPlatformLinks(ctx, u.ID)
		if err != nil {
			return nil, fmt.Errorf("listing platform links: %w", err)
		}
		data = append(data, userToAPI(u, links))
	}
	return &gen.UsersListOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) UsersGet(ctx context.Context, params gen.UsersGetParams) (*gen.UsersGetOK, error) {
	u, err := h.userStore.Get(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("not found")
	}
	links, err := h.userStore.ListPlatformLinks(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("listing platform links: %w", err)
	}
	return &gen.UsersGetOK{Data: userToAPI(*u, links)}, nil
}

func (h *AdminHandler) UsersUpdate(ctx context.Context, req *gen.UpdateUserRequest, params gen.UsersUpdateParams) (*gen.OkResponse, error) {
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
		return nil, fmt.Errorf("internal error")
	}
	return &gen.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) UsersGuilds(ctx context.Context, params gen.UsersGuildsParams) (*gen.UsersGuildsOK, error) {
	guilds, err := h.userStore.GetUserGuilds(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.UserGuild, 0, len(guilds))
	for _, g := range guilds {
		data = append(data, gen.UserGuild{
			GuildID:     g.GuildID,
			GuildName:   g.GuildName,
			ChannelID:   g.ChannelID,
			ChannelName: g.ChannelName,
			LastSeenAt:  g.LastSeenAt.Format("2006-01-02 15:04:05"),
		})
	}
	return &gen.UsersGuildsOK{Data: data}, nil
}

func (h *AdminHandler) UsersMemories(ctx context.Context, params gen.UsersMemoriesParams) (*gen.UsersMemoriesOK, error) {
	limit := int(params.Limit.Or(20))
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, content, created_at, updated_at FROM memories
		 WHERE type = 'user' AND metadata->>'user_id' = $1
		 ORDER BY updated_at DESC LIMIT $2`, params.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	var data []gen.UserMemory
	for rows.Next() {
		var e gen.UserMemory
		if err := rows.Scan(&e.ID, &e.Content, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		data = append(data, e)
	}
	if data == nil {
		data = []gen.UserMemory{}
	}
	return &gen.UsersMemoriesOK{Data: data}, nil
}
