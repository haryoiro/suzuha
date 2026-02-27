package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
)

// GuildsHandler provides HTTP handlers for guild data.
type GuildsHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewGuildsHandler creates a new GuildsHandler.
func NewGuildsHandler(db *sql.DB, logger *slog.Logger) *GuildsHandler {
	return &GuildsHandler{db: db, logger: logger}
}

// List handles GET /api/guilds.
func (h *GuildsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT g.id, g.name, g.updated_at,
		        COUNT(DISTINCT ugc.user_id) AS member_count,
		        COUNT(DISTINCT ugc.channel_id) AS channel_count
		 FROM guilds g
		 LEFT JOIN user_guild_channels ugc ON ugc.guild_id = g.id
		 GROUP BY g.id
		 ORDER BY g.updated_at DESC`)
	if err != nil {
		h.logger.Error("list guilds", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type guildJSON struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		UpdatedAt    string `json:"updated_at"`
		MemberCount  int    `json:"member_count"`
		ChannelCount int    `json:"channel_count"`
	}
	var guilds []guildJSON
	for rows.Next() {
		var g guildJSON
		if err := rows.Scan(&g.ID, &g.Name, &g.UpdatedAt, &g.MemberCount, &g.ChannelCount); err != nil {
			continue
		}
		guilds = append(guilds, g)
	}
	if guilds == nil {
		guilds = []guildJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": guilds})
}

// AllChannels handles GET /api/channels — flat list of all known channels with guild info.
func (h *GuildsHandler) AllChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT ugc.channel_id, ugc.channel_name, ugc.guild_id, g.name
		 FROM user_guild_channels ugc
		 JOIN guilds g ON g.id = ugc.guild_id
		 GROUP BY ugc.channel_id
		 ORDER BY g.name, ugc.channel_name`)
	if err != nil {
		h.logger.Error("all channels", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type entry struct {
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		GuildID     string `json:"guild_id"`
		GuildName   string `json:"guild_name"`
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ChannelID, &e.ChannelName, &e.GuildID, &e.GuildName); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

// Channels handles GET /api/guilds/{id}/channels.
func (h *GuildsHandler) Channels(w http.ResponseWriter, r *http.Request) {
	guildID := r.PathValue("id")
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT ugc.channel_id, ugc.channel_name,
		        COUNT(DISTINCT ugc.user_id) AS user_count,
		        MAX(ugc.last_seen_at) AS last_seen_at,
		        ca.last_user_message_at
		 FROM user_guild_channels ugc
		 LEFT JOIN channel_activity ca ON ca.channel_id = ugc.channel_id
		 WHERE ugc.guild_id = ?
		 GROUP BY ugc.channel_id
		 ORDER BY last_seen_at DESC`, guildID)
	if err != nil {
		h.logger.Error("guild channels", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type channelJSON struct {
		ChannelID        string  `json:"channel_id"`
		ChannelName      string  `json:"channel_name"`
		UserCount        int     `json:"user_count"`
		LastSeenAt       string  `json:"last_seen_at"`
		LastUserMessage  *string `json:"last_user_message_at"`
	}
	var channels []channelJSON
	for rows.Next() {
		var c channelJSON
		var lastMsg sql.NullString
		if err := rows.Scan(&c.ChannelID, &c.ChannelName, &c.UserCount, &c.LastSeenAt, &lastMsg); err != nil {
			continue
		}
		if lastMsg.Valid {
			c.LastUserMessage = &lastMsg.String
		}
		channels = append(channels, c)
	}
	if channels == nil {
		channels = []channelJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": channels})
}
