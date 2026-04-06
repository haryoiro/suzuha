package channel

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Mode represents the bot's behavior in a channel.
type Mode string

// Channel behavior modes.
const (
	ModeActive   Mode = "active"   // read and respond (default)
	ModeListen   Mode = "listen"   // ingest messages but never respond
	ModeDisabled Mode = "disabled" // completely ignore
)

// Settings holds per-channel configuration.
type Settings struct {
	ChannelID   string    `json:"channel_id"`
	GuildID     string    `json:"guild_id"`
	Mode        Mode      `json:"mode"`
	Home        bool      `json:"home"` // bot's home channel — posts monologues here
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store provides CRUD for channel settings with an in-memory cache.
type Store struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache map[string]*Settings
}

// NewStore creates a new channel settings Store.
func NewStore(db *sql.DB) *Store {
	return &Store{
		db:    db,
		cache: make(map[string]*Settings),
	}
}

// Get returns the settings for a channel. If no settings exist, returns
// a default Settings with ModeActive.
func (s *Store) Get(channelID string) Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cs, ok := s.cache[channelID]; ok {
		return *cs
	}
	return Settings{ChannelID: channelID, Mode: ModeActive}
}

// GetMode returns just the mode for a channel (convenience for hot path).
func (s *Store) GetMode(channelID string) Mode {
	return s.Get(channelID).Mode
}

// Set upserts channel settings to DB and updates the cache.
func (s *Store) Set(ctx context.Context, cs *Settings) error {
	if cs.Mode == "" {
		cs.Mode = ModeActive
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

// Delete removes channel settings, reverting the channel to default behavior.
func (s *Store) Delete(ctx context.Context, channelID string) error {
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

// List returns all channel settings, optionally filtered by guild.
func (s *Store) List(ctx context.Context, guildID string) ([]Settings, error) {
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

	var result []Settings
	for rows.Next() {
		var cs Settings
		var mode string
		if err := rows.Scan(&cs.ChannelID, &cs.GuildID, &mode, &cs.Home, &cs.UpdatedAt); err != nil {
			continue
		}
		cs.Mode = Mode(mode)
		result = append(result, cs)
	}
	return result, nil
}

// HomeChannelID returns the channel ID marked as home, or "" if none.
func (s *Store) HomeChannelID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, cs := range s.cache {
		if cs.Home {
			return cs.ChannelID
		}
	}
	return ""
}

// Reload refreshes the in-memory cache from the database.
func (s *Store) Reload(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, guild_id, mode, home, updated_at
		 FROM channel_settings`)
	if err != nil {
		return err
	}
	defer rows.Close()

	newCache := make(map[string]*Settings)
	for rows.Next() {
		var cs Settings
		var mode string
		if err := rows.Scan(&cs.ChannelID, &cs.GuildID, &mode, &cs.Home, &cs.UpdatedAt); err != nil {
			continue
		}
		cs.Mode = Mode(mode)
		newCache[cs.ChannelID] = &cs
	}

	s.mu.Lock()
	s.cache = newCache
	s.mu.Unlock()
	return nil
}
