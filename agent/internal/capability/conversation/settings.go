package conversation

import (
	"context"
	"database/sql"
	"sync"
	"time"

	domainchannel "github.com/haryoiro/suzuha/internal/domain/channel"
)

// SettingsStore はチャンネル設定の CRUD をインメモリキャッシュ付きで提供する。
type SettingsStore struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]*domainchannel.Settings
}

// NewSettingsStore は SettingsStore を生成する。
func NewSettingsStore(db *sql.DB) *SettingsStore {
	return &SettingsStore{
		db:    db,
		cache: make(map[string]*domainchannel.Settings),
	}
}

// Get は指定チャンネルの設定を返す。未設定なら既定 (ModeActive) を返す。
func (s *SettingsStore) Get(channelID string) domainchannel.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cs, ok := s.cache[channelID]; ok {
		return *cs
	}
	return domainchannel.Settings{ChannelID: channelID, Mode: domainchannel.ModeActive}
}

// GetMode は hot path 用にモードだけを取り出す。
func (s *SettingsStore) GetMode(channelID string) domainchannel.Mode {
	return s.Get(channelID).Mode
}

// Set はチャンネル設定を upsert しキャッシュを更新する。
func (s *SettingsStore) Set(ctx context.Context, cs *domainchannel.Settings) error {
	if cs.Mode == "" {
		cs.Mode = domainchannel.ModeActive
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channel_settings (channel_id, guild_id, mode, home, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   mode = excluded.mode,
		   home = excluded.home,
		   updated_at = excluded.updated_at`,
		cs.ChannelID, cs.GuildID, string(cs.Mode), cs.Home, now)
	if err != nil {
		return err
	}
	cs.UpdatedAt = now

	s.mu.Lock()
	s.cache[cs.ChannelID] = cs
	s.mu.Unlock()
	return nil
}

// Delete は指定チャンネルの設定を削除し既定挙動に戻す。
func (s *SettingsStore) Delete(ctx context.Context, channelID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM channel_settings WHERE channel_id = $1`, channelID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.cache, channelID)
	s.mu.Unlock()
	return nil
}

// List は全チャンネル設定を返す。guildID が空でなければ該当 guild のみ。
func (s *SettingsStore) List(ctx context.Context, guildID string) ([]domainchannel.Settings, error) {
	var rows *sql.Rows
	var err error
	if guildID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT channel_id, guild_id, mode, home, updated_at
			 FROM channel_settings WHERE guild_id = $1 ORDER BY updated_at DESC`, guildID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT channel_id, guild_id, mode, home, updated_at
			 FROM channel_settings ORDER BY updated_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domainchannel.Settings
	for rows.Next() {
		var cs domainchannel.Settings
		var mode string
		if err := rows.Scan(&cs.ChannelID, &cs.GuildID, &mode, &cs.Home, &cs.UpdatedAt); err != nil {
			continue
		}
		cs.Mode = domainchannel.Mode(mode)
		result = append(result, cs)
	}
	return result, nil
}

// HomeChannelID は home 指定されたチャンネル ID を返す。無ければ空文字列。
func (s *SettingsStore) HomeChannelID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, cs := range s.cache {
		if cs.Home {
			return cs.ChannelID
		}
	}
	return ""
}

// Reload はインメモリキャッシュを DB から再構築する。
func (s *SettingsStore) Reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, guild_id, mode, home, updated_at
		 FROM channel_settings`)
	if err != nil {
		return err
	}
	defer rows.Close()

	newCache := make(map[string]*domainchannel.Settings)
	for rows.Next() {
		var cs domainchannel.Settings
		var mode string
		if err := rows.Scan(&cs.ChannelID, &cs.GuildID, &mode, &cs.Home, &cs.UpdatedAt); err != nil {
			continue
		}
		cs.Mode = domainchannel.Mode(mode)
		newCache[cs.ChannelID] = &cs
	}

	s.mu.Lock()
	s.cache = newCache
	s.mu.Unlock()
	return nil
}
