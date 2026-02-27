package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ChannelSettingsHandler provides HTTP handlers for channel settings.
type ChannelSettingsHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewChannelSettingsHandler creates a new ChannelSettingsHandler.
func NewChannelSettingsHandler(db *sql.DB, logger *slog.Logger) *ChannelSettingsHandler {
	return &ChannelSettingsHandler{db: db, logger: logger}
}

type channelSettingJSON struct {
	ChannelID          string  `json:"channel_id"`
	ChannelName        string  `json:"channel_name"`
	GuildID            string  `json:"guild_id"`
	GuildName          string  `json:"guild_name"`
	UserCount          int     `json:"user_count"`
	Mode               string  `json:"mode"`
	UseIdentity        bool    `json:"use_identity"`
	LastUserMessageAt  *string `json:"last_user_message_at"`
	SettingsUpdatedAt  *string `json:"settings_updated_at"`
}

// List handles GET /api/channel-settings?guild_id=xxx.
// Returns all known channels with their settings (LEFT JOIN for unconfigured channels).
func (h *ChannelSettingsHandler) List(w http.ResponseWriter, r *http.Request) {
	guildID := r.URL.Query().Get("guild_id")

	query := `
		SELECT ugc.channel_id, ugc.channel_name, ugc.guild_id,
		       COALESCE(g.name, '') AS guild_name,
		       COUNT(DISTINCT ugc.user_id) AS user_count,
		       COALESCE(cs.mode, 'active') AS mode,
		       COALESCE(cs.use_identity, 0) AS use_identity,
		       ca.last_user_message_at,
		       cs.updated_at AS settings_updated_at
		FROM user_guild_channels ugc
		LEFT JOIN guilds g ON g.id = ugc.guild_id
		LEFT JOIN channel_settings cs ON cs.channel_id = ugc.channel_id
		LEFT JOIN channel_activity ca ON ca.channel_id = ugc.channel_id`

	var rows *sql.Rows
	var err error
	if guildID != "" {
		query += ` WHERE ugc.guild_id = ?`
		query += ` GROUP BY ugc.channel_id ORDER BY guild_name, ugc.channel_name`
		rows, err = h.db.QueryContext(r.Context(), query, guildID)
	} else {
		query += ` GROUP BY ugc.channel_id ORDER BY guild_name, ugc.channel_name`
		rows, err = h.db.QueryContext(r.Context(), query)
	}
	if err != nil {
		h.logger.Error("list channel settings", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var entries []channelSettingJSON
	for rows.Next() {
		var e channelSettingJSON
		var lastMsg, settingsUpdated sql.NullString
		if err := rows.Scan(&e.ChannelID, &e.ChannelName, &e.GuildID, &e.GuildName,
			&e.UserCount, &e.Mode, &e.UseIdentity, &lastMsg, &settingsUpdated); err != nil {
			h.logger.Error("scan channel setting", "error", err)
			continue
		}
		if lastMsg.Valid {
			e.LastUserMessageAt = &lastMsg.String
		}
		if settingsUpdated.Valid {
			e.SettingsUpdatedAt = &settingsUpdated.String
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []channelSettingJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

// Upsert handles PUT /api/channel-settings/{channelId}.
func (h *ChannelSettingsHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelId")
	if channelID == "" {
		http.Error(w, `{"error":"channel_id required"}`, http.StatusBadRequest)
		return
	}

	var body struct {
		Mode        string `json:"mode"`
		UseIdentity bool   `json:"use_identity"`
		GuildID     string `json:"guild_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	// Validate mode.
	switch body.Mode {
	case "active", "listen", "disabled":
		// ok
	default:
		http.Error(w, `{"error":"mode must be active, listen, or disabled"}`, http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO channel_settings (channel_id, guild_id, mode, use_identity, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   mode = excluded.mode,
		   use_identity = excluded.use_identity,
		   updated_at = excluded.updated_at`,
		channelID, body.GuildID, body.Mode, body.UseIdentity, now)
	if err != nil {
		h.logger.Error("upsert channel setting", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/channel-settings/{channelId}.
// Removes the override, reverting the channel to default (active) behavior.
func (h *ChannelSettingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channelId")
	if channelID == "" {
		http.Error(w, `{"error":"channel_id required"}`, http.StatusBadRequest)
		return
	}

	_, err := h.db.ExecContext(r.Context(),
		`DELETE FROM channel_settings WHERE channel_id = ?`, channelID)
	if err != nil {
		h.logger.Error("delete channel setting", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
