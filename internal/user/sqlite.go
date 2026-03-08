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
		`SELECT user_id FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
		platform, platformUserID,
	).Scan(&userID)

	if err == nil {
		// User exists — load and return.
		u, err := s.getInTx(ctx, tx, userID)
		if err != nil {
			// Orphaned platform_link: link exists but user row is missing.
			// Delete the stale link and fall through to create a new user.
			if _, delErr := tx.ExecContext(ctx,
				`DELETE FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
				platform, platformUserID,
			); delErr != nil {
				return nil, fmt.Errorf("user: 孤立リンクの削除に失敗: %w", delErr)
			}
			goto createUser
		}
		// If this is a known bot ID but the user wasn't marked yet, fix it.
		if s.botUserIDs[platformUserID] && !u.IsBot {
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET is_bot = 1, updated_at = ? WHERE id = ?`,
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
		Affinity:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, display_name, role, is_bot, affinity, created_at, updated_at)
		 VALUES (?, '', ?, ?, 0.0, ?, ?)`,
		u.ID, string(u.Role), u.IsBot, u.CreatedAt, u.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("user: ユーザーの挿入に失敗: %w", err)
	}

	linkID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO platform_links (id, user_id, platform, platform_user_id, platform_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
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
		`SELECT id, display_name, role, is_bot, affinity, closeness, trust, interest, metadata, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.DisplayName, &roleStr, &u.IsBot, &u.Affinity,
		&u.Closeness, &u.Trust, &u.Interest,
		&metaJSON, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("user: ユーザー取得に失敗: %w", err)
	}
	u.Role = Role(roleStr)
	if metaJSON.Valid {
		_ = json.Unmarshal([]byte(metaJSON.String), &u.Metadata)
	}
	return &u, nil
}

func (s *SQLiteStore) UpdateDisplayName(ctx context.Context, userID, displayName string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
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

func (s *SQLiteStore) UpdateAffinity(ctx context.Context, evt *AffinityEvent) error {
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now()
	}
	if evt.Axis == "" {
		evt.Axis = AxisCloseness
	}

	interactionJSON, _ := json.Marshal(evt.InteractionIDs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	// Insert the affinity event with axis.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO affinity_events (id, user_id, delta, axis, reason, interaction_ids, group_start, group_end, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.ID, evt.UserID, evt.Delta, string(evt.Axis), evt.Reason,
		string(interactionJSON),
		nullTime(evt.GroupStart), nullTime(evt.GroupEnd),
		evt.CreatedAt,
	); err != nil {
		return fmt.Errorf("user: 親密度イベントの挿入に失敗: %w", err)
	}

	// Recalculate effective value for this user+axis from all events.
	if err := s.recalcUserAxis(ctx, tx, evt.UserID, evt.Axis); err != nil {
		return fmt.Errorf("user: 実効値の再計算に失敗: %w", err)
	}

	return tx.Commit()
}

// weightedSumSQL is the SQL expression for computing a time-decay weighted sum
// of affinity deltas. Uses the Weight* constants from user.go.
var weightedSumSQL = fmt.Sprintf(`SELECT COALESCE(SUM(delta * CASE
	WHEN julianday('now') - julianday(created_at) <= 7 THEN %v
	WHEN julianday('now') - julianday(created_at) <= 28 THEN %v
	WHEN julianday('now') - julianday(created_at) <= 90 THEN %v
	ELSE %v
END), 0.0) FROM affinity_events WHERE user_id = ? AND axis = ?`,
	WeightRecent, WeightMonth, WeightQuarter, WeightOld)

// axisColumn maps each AffinityAxis to its column name in the users table.
var axisColumn = map[AffinityAxis]string{
	AxisCloseness: "closeness",
	AxisTrust:     "trust",
	AxisInterest:  "interest",
}

// allAxes is the ordered list of affinity axes.
var allAxes = []AffinityAxis{AxisCloseness, AxisTrust, AxisInterest}

// recalcUserAxis recalculates the effective value for a single user+axis
// and updates the users table within the given transaction.
func (s *SQLiteStore) recalcUserAxis(ctx context.Context, tx *sql.Tx, userID string, axis AffinityAxis) error {
	col := axisColumn[axis]

	var weighted float64
	if err := tx.QueryRowContext(ctx, weightedSumSQL, userID, string(axis)).Scan(&weighted); err != nil {
		return err
	}
	effective := EffectiveValue(weighted)

	// Read the other two axes' current values for legacy affinity sum.
	var otherCols [2]string
	idx := 0
	for _, a := range allAxes {
		if a != axis {
			otherCols[idx] = axisColumn[a]
			idx++
		}
	}

	var other1, other2 float64
	row := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT %s, %s FROM users WHERE id = ?`, otherCols[0], otherCols[1]),
		userID)
	if err := row.Scan(&other1, &other2); err != nil {
		return err
	}

	now := time.Now()
	query := fmt.Sprintf(
		`UPDATE users SET %s = ?, affinity = ?, updated_at = ? WHERE id = ?`,
		col,
	)
	if _, err := tx.ExecContext(ctx, query, effective, effective+other1+other2, now, userID); err != nil {
		return err
	}
	return nil
}

// RecalculateEffective recomputes effective affinity values for all users
// from their event history, applying time-based decay and soft cap.
func (s *SQLiteStore) RecalculateEffective(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT user_id FROM affinity_events`)
	if err != nil {
		return fmt.Errorf("user: ユーザー一覧の取得に失敗: %w", err)
	}
	var userIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("user: ユーザーIDのスキャンに失敗: %w", err)
		}
		userIDs = append(userIDs, id)
	}
	rows.Close()

	for _, uid := range userIDs {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("user: トランザクション開始に失敗: %w", err)
		}
		for _, axis := range []AffinityAxis{AxisCloseness, AxisTrust, AxisInterest} {
			if err := s.recalcUserAxis(ctx, tx, uid, axis); err != nil {
				tx.Rollback()
				return fmt.Errorf("user: %s/%s の再計算に失敗: %w", uid, axis, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("user: コミットに失敗: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) GetAffinity(ctx context.Context, userID string, limit int) ([]AffinityEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, delta, axis, reason, interaction_ids, group_start, group_end, created_at
		 FROM affinity_events WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("user: 親密度の取得に失敗: %w", err)
	}
	defer rows.Close()

	var events []AffinityEvent
	for rows.Next() {
		var e AffinityEvent
		var axisStr string
		var idsJSON sql.NullString
		var groupStart, groupEnd sql.NullTime
		if err := rows.Scan(&e.ID, &e.UserID, &e.Delta, &axisStr, &e.Reason, &idsJSON, &groupStart, &groupEnd, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: 親密度のスキャンに失敗: %w", err)
		}
		e.Axis = AffinityAxis(axisStr)
		if idsJSON.Valid {
			_ = json.Unmarshal([]byte(idsJSON.String), &e.InteractionIDs)
		}
		if groupStart.Valid {
			e.GroupStart = groupStart.Time
		}
		if groupEnd.Valid {
			e.GroupEnd = groupEnd.Time
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) TrackGuildChannel(ctx context.Context, userID, guildID, guildName, channelID, channelName string) error {
	if guildID == "" || channelID == "" {
		return nil
	}
	now := time.Now()

	// Upsert guild name.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO guilds (id, name, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		guildID, guildName, now,
	); err != nil {
		return fmt.Errorf("user: ギルドのupsertに失敗: %w", err)
	}

	// Upsert user-guild-channel association.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_guild_channels (user_id, guild_id, channel_id, channel_name, last_seen_at)
		 VALUES (?, ?, ?, ?, ?)
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
		 WHERE ugc.user_id = ?
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
		`SELECT user_id FROM platform_links WHERE platform = ? AND platform_user_id = ?`,
		platform, platformUserID,
	).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("user: 既存ユーザーの解決に失敗: %w", err)
	}
	return s.Get(ctx, userID)
}

func (s *SQLiteStore) ListMentionable(ctx context.Context) ([]MentionableUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.display_name, pl.platform_user_id, u.affinity, u.closeness, u.interest
		FROM users u
		JOIN platform_links pl ON pl.user_id = u.id AND pl.platform = 'discord'
		WHERE u.is_bot = 0 AND u.affinity > 0
		ORDER BY u.interest DESC, u.closeness DESC`)
	if err != nil {
		return nil, fmt.Errorf("user: メンション可能ユーザーの一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var result []MentionableUser
	for rows.Next() {
		var m MentionableUser
		if err := rows.Scan(&m.DisplayName, &m.DiscordUserID, &m.Affinity, &m.Closeness, &m.Interest); err != nil {
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
		`SELECT id, display_name, role, is_bot, affinity, closeness, trust, interest, metadata, created_at, updated_at
		 FROM users ORDER BY updated_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("user: 一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var roleStr string
		var metaJSON sql.NullString
		if err := rows.Scan(&u.ID, &u.DisplayName, &roleStr, &u.IsBot, &u.Affinity,
			&u.Closeness, &u.Trust, &u.Interest,
			&metaJSON, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("user: 一覧のスキャンに失敗: %w", err)
		}
		u.Role = Role(roleStr)
		if metaJSON.Valid {
			_ = json.Unmarshal([]byte(metaJSON.String), &u.Metadata)
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, id string, fields UpdateFields) error {
	var sets []string
	var args []any
	if fields.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, *fields.DisplayName)
	}
	if fields.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, string(*fields.Role))
	}
	if fields.IsBot != nil {
		sets = append(sets, "is_bot = ?")
		args = append(args, *fields.IsBot)
	}
	if len(sets) == 0 {
		return fmt.Errorf("user: 更新するフィールドがありません")
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now())
	args = append(args, id)

	query := "UPDATE users SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += " WHERE id = ?"

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("user: 更新に失敗: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user: 見つかりません: %s", id)
	}
	return nil
}

func (s *SQLiteStore) ListPlatformLinks(ctx context.Context, userID string) ([]PlatformLink, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, platform, platform_user_id, platform_name, created_at
		 FROM platform_links WHERE user_id = ?`, userID)
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

func (s *SQLiteStore) ListAffinityEvents(ctx context.Context, userID string, limit int) ([]AffinityEvent, error) {
	return s.GetAffinity(ctx, userID, limit)
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
		WHERE ugc.guild_id = ?
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
