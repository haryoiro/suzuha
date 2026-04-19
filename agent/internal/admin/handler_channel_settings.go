package admin

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

func (h *AdminHandler) ChannelSettingsList(ctx context.Context, params api.ChannelSettingsListParams) (*api.ChannelSettingsListOK, error) {
	guildID := params.GuildID.Or("")

	query := `
		SELECT ugc.channel_id, MAX(ugc.channel_name) AS channel_name, ugc.guild_id,
		       COALESCE(MAX(g.name), '') AS guild_name,
		       COUNT(DISTINCT ugc.user_id) AS user_count,
		       COALESCE(MAX(cs.mode), 'active') AS mode,
		       COALESCE(BOOL_OR(cs.home), false) AS home,
		       MAX(ca.last_user_message_at) AS last_user_message_at,
		       MAX(cs.updated_at) AS settings_updated_at
		FROM user_guild_channels ugc
		LEFT JOIN guilds g ON g.id = ugc.guild_id
		LEFT JOIN channel_settings cs ON cs.channel_id = ugc.channel_id
		LEFT JOIN channel_activity ca ON ca.channel_id = ugc.channel_id`

	var rows *sql.Rows
	var err error
	if guildID != "" {
		query += ` WHERE ugc.guild_id = $1`
		query += ` GROUP BY ugc.channel_id, ugc.guild_id ORDER BY guild_name, channel_name`
		rows, err = h.db.QueryContext(ctx, query, guildID)
	} else {
		query += ` GROUP BY ugc.channel_id, ugc.guild_id ORDER BY guild_name, channel_name`
		rows, err = h.db.QueryContext(ctx, query)
	}
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	var entries []api.ChannelSetting
	for rows.Next() {
		var e api.ChannelSetting
		var mode string
		var lastMsg, settingsUpdated sql.NullTime
		if err := rows.Scan(&e.ChannelID, &e.ChannelName, &e.GuildID, &e.GuildName,
			&e.UserCount, &mode, &e.Home, &lastMsg, &settingsUpdated); err != nil {
			h.logger.Error("チャンネル設定のスキャンに失敗", "error", err.Error())
			continue
		}
		e.Mode = api.ChannelSettingMode(mode)
		if lastMsg.Valid {
			e.LastUserMessageAt = api.NewOptString(lastMsg.Time.Format("2006-01-02 15:04:05"))
		}
		if settingsUpdated.Valid {
			e.SettingsUpdatedAt = api.NewOptString(settingsUpdated.Time.Format("2006-01-02 15:04:05"))
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []api.ChannelSetting{}
	}
	return &api.ChannelSettingsListOK{Data: entries}, nil
}

func (h *AdminHandler) ChannelSettingsUpdate(ctx context.Context, req *api.UpdateChannelSettingRequest, params api.ChannelSettingsUpdateParams) (*api.OkResponse, error) {
	mode := string(req.Mode.Or(api.UpdateChannelSettingRequestModeActive))
	home := req.Home.Or(false)
	guildID := req.GuildID.Or("")

	now := time.Now()
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO channel_settings (channel_id, guild_id, mode, home, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   mode = excluded.mode,
		   home = excluded.home,
		   updated_at = excluded.updated_at`,
		params.ChannelId, guildID, mode, home, now)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	h.notifyAgentReload(ctx, "/internal/reload-channel-settings")
	return &api.OkResponse{Ok: true}, nil
}

// deleteChannel removes all DB records for a channel and notifies the agent.
// deleteChannelByID はチャンネルに関連する全データを DB から削除する。
func (h *AdminHandler) deleteChannelByID(ctx context.Context, channelID string) {
	h.db.ExecContext(ctx, `DELETE FROM channel_settings WHERE channel_id = $1`, channelID)
	h.db.ExecContext(ctx, `DELETE FROM channel_activity WHERE channel_id = $1`, channelID)
	h.db.ExecContext(ctx, `DELETE FROM conversation_logs WHERE channel_id = $1`, channelID)
	h.db.ExecContext(ctx, `DELETE FROM user_guild_channels WHERE channel_id = $1`, channelID)
	h.notifyAgentReload(ctx, "/internal/reload-channel-settings")
}

func (h *AdminHandler) ChannelSettingsDelete(ctx context.Context, params api.ChannelSettingsDeleteParams) error {
	_, err := h.db.ExecContext(ctx,
		`DELETE FROM channel_settings WHERE channel_id = $1`, params.ChannelId)
	if err != nil {
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-channel-settings")
	return nil
}
