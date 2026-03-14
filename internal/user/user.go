package user

import (
	"context"
	"time"
)

// Role represents a user's permission level.
type Role string

// Role constants define the available permission levels.
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

// User is an internal user identity.
type User struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        Role           `json:"role"`
	IsBot       bool           `json:"is_bot"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// PlatformLink connects an internal user to a platform identity.
type PlatformLink struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	PlatformName   string    `json:"platform_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// UserGuild represents a guild+channel association for a user.
type UserGuild struct {
	GuildID     string    `json:"guild_id"`
	GuildName   string    `json:"guild_name"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// Store is the user storage interface.
type Store interface {
	// Resolve looks up an internal user by platform + platform_user_id.
	// If the user does not exist, it auto-creates one and links them.
	// CLI platform users are created with RoleOwner.
	Resolve(ctx context.Context, platform, platformUserID, platformName string) (*User, error)

	// Get returns a user by internal ID.
	Get(ctx context.Context, id string) (*User, error)

	// UpdateDisplayName changes the user's nickname.
	UpdateDisplayName(ctx context.Context, userID, displayName string) error

	// TrackGuildChannel records that a user was seen in a guild+channel.
	TrackGuildChannel(ctx context.Context, userID, guildID, guildName, channelID, channelName string) error

	// GetUserGuilds returns guilds and channels a user has been seen in.
	GetUserGuilds(ctx context.Context, userID string) ([]UserGuild, error)

	// ResolveExisting looks up an internal user by platform + platform_user_id.
	// Returns sql.ErrNoRows (wrapped) if the user does not exist.
	// Unlike Resolve, this does NOT create the user.
	ResolveExisting(ctx context.Context, platform, platformUserID string) (*User, error)

	// ListMentionable returns non-bot users
	// who have a discord platform link. Used for mention targeting.
	ListMentionable(ctx context.Context) ([]MentionableUser, error)

	// Close releases resources.
	Close() error
}

// AdminStore extends Store with methods needed by the admin dashboard.
type AdminStore interface {
	Store

	// List returns users with pagination.
	List(ctx context.Context, offset, limit int) ([]User, int, error)

	// Update applies partial updates to a user.
	// Only non-nil fields are updated.
	Update(ctx context.Context, id string, fields UpdateFields) error

	// ListPlatformLinks returns all platform links for a user.
	ListPlatformLinks(ctx context.Context, userID string) ([]PlatformLink, error)

	// ListGuilds returns all guilds with member and channel counts.
	ListGuilds(ctx context.Context) ([]GuildSummary, error)

	// ListAllChannels returns all channels with their guild info.
	ListAllChannels(ctx context.Context) ([]ChannelEntry, error)

	// GetGuildChannels returns channels for a specific guild with activity info.
	GetGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error)
}

// UpdateFields holds the optional update fields for admin Update.
type UpdateFields struct {
	DisplayName *string
	Role        *Role
	IsBot       *bool
}

// MentionableUser is a lightweight read model for mention targeting.
type MentionableUser struct {
	DisplayName   string `json:"display_name"`
	DiscordUserID string `json:"discord_user_id"`
}

// GuildSummary is a read model for the guilds list admin view.
type GuildSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	UpdatedAt    time.Time `json:"updated_at"`
	MemberCount  int       `json:"member_count"`
	ChannelCount int       `json:"channel_count"`
}

// ChannelEntry is a flat channel+guild record.
type ChannelEntry struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	GuildID     string `json:"guild_id"`
	GuildName   string `json:"guild_name"`
}

// GuildChannel combines channel data with activity info.
type GuildChannel struct {
	ChannelID         string  `json:"channel_id"`
	ChannelName       string  `json:"channel_name"`
	UserCount         int     `json:"user_count"`
	LastSeenAt        string  `json:"last_seen_at"`
	LastUserMessageAt *string `json:"last_user_message_at,omitempty"`
}
