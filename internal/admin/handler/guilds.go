package handler

import (
	"log/slog"
	"net/http"

	"github.com/haryoiro/suzuha/internal/user"
)

// GuildsHandler provides HTTP handlers for guild data.
type GuildsHandler struct {
	users  user.AdminStore
	logger *slog.Logger
}

// NewGuildsHandler creates a new GuildsHandler.
func NewGuildsHandler(users user.AdminStore, logger *slog.Logger) *GuildsHandler {
	return &GuildsHandler{users: users, logger: logger}
}

// List handles GET /api/guilds.
func (h *GuildsHandler) List(w http.ResponseWriter, r *http.Request) {
	guilds, err := h.users.ListGuilds(r.Context())
	if err != nil {
		h.logger.Error("ギルド一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	type guildJSON struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		UpdatedAt    string `json:"updated_at"`
		MemberCount  int    `json:"member_count"`
		ChannelCount int    `json:"channel_count"`
	}
	result := make([]guildJSON, 0, len(guilds))
	for _, g := range guilds {
		result = append(result, guildJSON{
			ID:           g.ID,
			Name:         g.Name,
			UpdatedAt:    g.UpdatedAt.Format("2006-01-02 15:04:05"),
			MemberCount:  g.MemberCount,
			ChannelCount: g.ChannelCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

// AllChannels handles GET /api/channels — flat list of all known channels with guild info.
func (h *GuildsHandler) AllChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.users.ListAllChannels(r.Context())
	if err != nil {
		h.logger.Error("全チャンネル一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	type entry struct {
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		GuildID     string `json:"guild_id"`
		GuildName   string `json:"guild_name"`
	}
	result := make([]entry, 0, len(channels))
	for _, c := range channels {
		result = append(result, entry{
			ChannelID:   c.ChannelID,
			ChannelName: c.ChannelName,
			GuildID:     c.GuildID,
			GuildName:   c.GuildName,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

// Channels handles GET /api/guilds/{id}/channels.
func (h *GuildsHandler) Channels(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("id")

	channels, err := h.users.GetGuildChannels(r.Context(), guildID)
	if err != nil {
		h.logger.Error("ギルドのチャンネル取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	type channelJSON struct {
		ChannelID       string  `json:"channel_id"`
		ChannelName     string  `json:"channel_name"`
		UserCount       int     `json:"user_count"`
		LastSeenAt      string  `json:"last_seen_at"`
		LastUserMessage *string `json:"last_user_message_at"`
	}
	result := make([]channelJSON, 0, len(channels))
	for _, c := range channels {
		result = append(result, channelJSON{
			ChannelID:       c.ChannelID,
			ChannelName:     c.ChannelName,
			UserCount:       c.UserCount,
			LastSeenAt:      c.LastSeenAt,
			LastUserMessage: c.LastUserMessageAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}
