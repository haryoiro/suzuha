package user

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SQLiteStore implements Store using the shared SQLite database.
type SQLiteStore struct {
	db         *sql.DB
	botUserIDs map[string]bool // platform user IDs that belong to the bot itself
}

// NewSQLiteStore creates a user store that shares the given database connection.
// botPlatformUserIDs are platform user IDs (e.g. Discord user ID) that identify
// the bot itself. Users resolved with these IDs are marked as is_bot=true.
func NewSQLiteStore(db *sql.DB, botPlatformUserIDs ...string) *SQLiteStore {
	ids := make(map[string]bool, len(botPlatformUserIDs))
	for _, id := range botPlatformUserIDs {
		if id != "" {
			ids[id] = true
		}
	}
	return &SQLiteStore{db: db, botUserIDs: ids}
}

// AddBotID registers an additional platform user ID as belonging to the bot.
// This is used when the actual bot ID is only known at runtime (e.g. after Discord connects).
func (s *SQLiteStore) AddBotID(platformUserID string) {
	if platformUserID != "" {
		s.botUserIDs[platformUserID] = true
	}
}

func (s *SQLiteStore) Resolve(ctx context.Context, platform, platformUserID, platformName string) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("user: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	// Look up existing platform link.
	var userID string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM platform_links WHERE platform = $1 AND platform_user_id = $2`,
		platform, platformUserID,
	).Scan(&userID)

	if err == nil {
		// User exists — load and return.
		u, err := s.getInTx(ctx, tx, userID)
		if err != nil {
			// Orphaned platform_link: link exists but user row is missing.
			// Delete the stale link and fall through to create a new user.
			if _, delErr := tx.ExecContext(ctx,
				`DELETE FROM platform_links WHERE platform = $1 AND platform_user_id = $2`,
				platform, platformUserID,
			); delErr != nil {
				return nil, fmt.Errorf("user: 孤立リンクの削除に失敗: %w", delErr)
			}
			goto createUser
		}
		// If this is a known bot ID but the user wasn't marked yet, fix it.
		if s.botUserIDs[platformUserID] && !u.IsBot {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET is_bot = true, updated_at = $1 WHERE id = $2`,
				time.Now(), userID,
			); err != nil {
				return nil, fmt.Errorf("user: ボットフラグの設定に失敗: %w", err)
			}
			u.IsBot = true
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("user: コミットに失敗: %w", err)
		}
		return u, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("user: プラットフォームリンクの検索に失敗: %w", err)
	}

createUser:
	// User does not exist — create.
	isBot := s.botUserIDs[platformUserID]
	role := RoleMember
	switch {
	case isBot:
		role = RoleMember // bot gets member role, identified by is_bot flag
	case platform == "cli":
		role = RoleOwner
	}

	now := time.Now()
	u := &User{
		ID:        uuid.NewString(),
		Role:      role,
		IsBot:     isBot,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, display_name, role, is_bot, created_at, updated_at)
		 VALUES ($1, '', $2, $3, $4, $5)`,
		u.ID, string(u.Role), u.IsBot, u.CreatedAt, u.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("user: ユーザーの挿入に失敗: %w", err)
	}

	linkID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO platform_links (id, user_id, platform, platform_user_id, platform_name, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		linkID, u.ID, platform, platformUserID, platformName, now,
	); err != nil {
		return nil, fmt.Errorf("user: プラットフォームリンクの挿入に失敗: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("user: コミットに失敗: %w", err)
	}
	return u, nil
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*User, error) {
	return s.getFromDB(ctx, s.db, id)
}

func (s *SQLiteStore) getInTx(ctx context.Context, tx *sql.Tx, id string) (*User, error) {
	return s.getFromDB(ctx, tx, id)
}

type queryable interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLiteStore) getFromDB(ctx context.Context, q queryable, id string) (*User, error) {
	var u User
	var roleStr string
	var metaJSON sql.NullString

	err := q.QueryRowContext(ctx,
		`SELECT id, display_name, role, is_bot, metadata, created_at, updated_at
		 FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.DisplayName, &roleStr, &u.IsBot,
		&metaJSON, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("user: ユーザー取得に失敗: %w", err)
	}
	u.Role = Role(roleStr)
	if metaJSON.Valid {
		if err := json.Unmarshal([]byte(metaJSON.String), &u.Metadata); err != nil {
			return nil, fmt.Errorf("user: メタデータのパースに失敗: %w", err)
		}
	}
	return &u, nil
}

func (s *SQLiteStore) UpdateDisplayName(ctx context.Context, userID, displayName string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = $1, updated_at = $2 WHERE id = $3`,
		displayName, time.Now(), userID,
	)
	if err != nil {
		return fmt.Errorf("user: 表示名の更新に失敗: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user: 見つかりません: %s", userID)
	}
	return nil
}

func (s *SQLiteStore) TrackGuildChannel(ctx context.Context, userID, guildID, guildName, channelID, channelName string) error {
	if guildID == "" || channelID == "" {
		return nil
	}
	now := time.Now()

	// Upsert guild name.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO guilds (id, name, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		guildID, guildName, now,
	); err != nil {
		return fmt.Errorf("user: ギルドのupsertに失敗: %w", err)
	}

	// Upsert user-guild-channel association.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_guild_channels (user_id, guild_id, channel_id, channel_name, last_seen_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(user_id, guild_id, channel_id) DO UPDATE SET
		   channel_name = excluded.channel_name, last_seen_at = excluded.last_seen_at`,
		userID, guildID, channelID, channelName, now,
	); err != nil {
		return fmt.Errorf("user: ユーザーギルドチャンネルのupsertに失敗: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetUserGuilds(ctx context.Context, userID string) ([]UserGuild, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ugc.guild_id, g.name, ugc.channel_id, ugc.channel_name, ugc.last_seen_at
		 FROM user_guild_channels ugc
		 JOIN guilds g ON g.id = ugc.guild_id
		 WHERE ugc.user_id = $1
		 ORDER BY ugc.last_seen_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("user: ユーザーギルドの取得に失敗: %w", err)
	}
	defer rows.Close()

	var result []UserGuild
	for rows.Next() {
		var ug UserGuild
		if err := rows.Scan(&ug.GuildID, &ug.GuildName, &ug.ChannelID, &ug.ChannelName, &ug.LastSeenAt); err != nil {
			return nil, fmt.Errorf("user: ユーザーギルドのスキャンに失敗: %w", err)
		}
		result = append(result, ug)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ResolveExisting(ctx context.Context, platform, platformUserID string) (*User, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM platform_links WHERE platform = $1 AND platform_user_id = $2`,
		platform, platformUserID,
	).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("user: 既存ユーザーの解決に失敗: %w", err)
	}
	return s.Get(ctx, userID)
}

func (s *SQLiteStore) ListMentionable(ctx context.Context) ([]MentionableUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.display_name, pl.platform_user_id
		FROM users u
		JOIN platform_links pl ON pl.user_id = u.id AND pl.platform = 'discord'
		WHERE u.is_bot = false
		ORDER BY u.display_name`)
	if err != nil {
		return nil, fmt.Errorf("user: メンション可能ユーザーの一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var result []MentionableUser
	for rows.Next() {
		var m MentionableUser
		if err := rows.Scan(&m.DisplayName, &m.DiscordUserID); err != nil {
			return nil, fmt.Errorf("user: メンション可能ユーザーのスキャンに失敗: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// --- AdminStore implementation ---

func (s *SQLiteStore) List(ctx context.Context, offset, limit int) ([]User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("user: カウントに失敗: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, role, is_bot, metadata, created_at, updated_at
		 FROM users ORDER BY updated_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("user: 一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var roleStr string
		var metaJSON sql.NullString
		if err := rows.Scan(&u.ID, &u.DisplayName, &roleStr, &u.IsBot,
			&metaJSON, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("user: 一覧のスキャンに失敗: %w", err)
		}
		u.Role = Role(roleStr)
		if metaJSON.Valid {
			if err := json.Unmarshal([]byte(metaJSON.String), &u.Metadata); err != nil {
				return nil, 0, fmt.Errorf("user: メタデータのパースに失敗: %w", err)
			}
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, id string, fields UpdateFields) error {
	var sets []string
	var args []any
	n := 1
	if fields.DisplayName != nil {
		sets = append(sets, fmt.Sprintf("display_name = $%d", n))
		args = append(args, *fields.DisplayName)
		n++
	}
	if fields.Role != nil {
		sets = append(sets, fmt.Sprintf("role = $%d", n))
		args = append(args, string(*fields.Role))
		n++
	}
	if fields.IsBot != nil {
		sets = append(sets, fmt.Sprintf("is_bot = $%d", n))
		args = append(args, *fields.IsBot)
		n++
	}
	if len(sets) == 0 {
		return fmt.Errorf("user: 更新するフィールドがありません")
	}
	sets = append(sets, fmt.Sprintf("updated_at = $%d", n))
	args = append(args, time.Now())
	n++
	args = append(args, id)

	query := "UPDATE users SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += fmt.Sprintf(" WHERE id = $%d", n)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("user: 更新に失敗: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("user: 見つかりません: %s", id)
	}
	return nil
}

func (s *SQLiteStore) ListPlatformLinks(ctx context.Context, userID string) ([]PlatformLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, platform, platform_user_id, platform_name, created_at
		 FROM platform_links WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("user: プラットフォームリンク一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	var links []PlatformLink
	for rows.Next() {
		var l PlatformLink
		if err := rows.Scan(&l.ID, &l.UserID, &l.Platform, &l.PlatformUserID, &l.PlatformName, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: プラットフォームリンクのスキャンに失敗: %w", err)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func (s *SQLiteStore) ListGuilds(ctx context.Context) ([]GuildSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.updated_at,
		       COUNT(DISTINCT ugc.user_id) AS member_count,
		       COUNT(DISTINCT ugc.channel_id) AS channel_count
		FROM guilds g
		LEFT JOIN user_guild_channels ugc ON ugc.guild_id = g.id
		GROUP BY g.id
		ORDER BY g.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("user: ギルド一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	var guilds []GuildSummary
	for rows.Next() {
		var g GuildSummary
		if err := rows.Scan(&g.ID, &g.Name, &g.UpdatedAt, &g.MemberCount, &g.ChannelCount); err != nil {
			return nil, fmt.Errorf("user: ギルドのスキャンに失敗: %w", err)
		}
		guilds = append(guilds, g)
	}
	return guilds, rows.Err()
}

func (s *SQLiteStore) ListAllChannels(ctx context.Context) ([]ChannelEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ugc.channel_id, ugc.channel_name, ugc.guild_id, g.name
		FROM user_guild_channels ugc
		JOIN guilds g ON g.id = ugc.guild_id
		GROUP BY ugc.channel_id
		ORDER BY g.name, ugc.channel_name`)
	if err != nil {
		return nil, fmt.Errorf("user: 全チャンネル一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	var entries []ChannelEntry
	for rows.Next() {
		var e ChannelEntry
		if err := rows.Scan(&e.ChannelID, &e.ChannelName, &e.GuildID, &e.GuildName); err != nil {
			return nil, fmt.Errorf("user: チャンネルエントリのスキャンに失敗: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) GetGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ugc.channel_id, ugc.channel_name,
		       COUNT(DISTINCT ugc.user_id) AS user_count,
		       MAX(ugc.last_seen_at) AS last_seen_at,
		       ca.last_user_message_at
		FROM user_guild_channels ugc
		LEFT JOIN channel_activity ca ON ca.channel_id = ugc.channel_id
		WHERE ugc.guild_id = $1
		GROUP BY ugc.channel_id
		ORDER BY last_seen_at DESC`, guildID)
	if err != nil {
		return nil, fmt.Errorf("user: ギルドチャンネルの取得に失敗: %w", err)
	}
	defer rows.Close()

	var channels []GuildChannel
	for rows.Next() {
		var c GuildChannel
		var lastMsg sql.NullString
		if err := rows.Scan(&c.ChannelID, &c.ChannelName, &c.UserCount, &c.LastSeenAt, &lastMsg); err != nil {
			return nil, fmt.Errorf("user: ギルドチャンネルのスキャンに失敗: %w", err)
		}
		if lastMsg.Valid {
			c.LastUserMessageAt = &lastMsg.String
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func (s *SQLiteStore) Close() error {
	// DB is shared — don't close it here.
	return nil
}

// nullTime returns nil for zero time values (so SQLite stores NULL).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
