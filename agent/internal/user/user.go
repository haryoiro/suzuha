package user

import (
	"context"

	domain "github.com/haryoiro/suzuha/internal/domain/user"
)

// 以下は domain/user へ昇格済みの型に対する legacy エイリアス。
// 段階移行のため internal/user/ からも参照できるよう残してある。
// 正準定義は domain/user/ にあり、Phase 5 で本 package を adapter/store/user/ に
// 分解した時点で本エイリアス群は不要になる。
type (
	// Role は domain/user.Role のエイリアス。
	Role = domain.Role
	// User は domain/user.User のエイリアス。
	User = domain.User
	// PlatformLink は domain/user.PlatformLink のエイリアス。
	PlatformLink = domain.PlatformLink
	// UserGuild は domain/user.UserGuild のエイリアス。
	UserGuild = domain.UserGuild
	// UpdateFields は domain/user.UpdateFields のエイリアス。
	UpdateFields = domain.UpdateFields
	// MentionableUser は domain/user.MentionableUser のエイリアス。
	MentionableUser = domain.MentionableUser
	// GuildSummary は domain/user.GuildSummary のエイリアス。
	GuildSummary = domain.GuildSummary
	// ChannelEntry は domain/user.ChannelEntry のエイリアス。
	ChannelEntry = domain.ChannelEntry
	// GuildChannel は domain/user.GuildChannel のエイリアス。
	GuildChannel = domain.GuildChannel
)

// Role 定数は domain/user の値を再エクスポート。
const (
	RoleOwner  = domain.RoleOwner
	RoleMember = domain.RoleMember
	RoleGuest  = domain.RoleGuest
)

// BotRegistrar は起動後に判明する bot の platform user ID を登録する。
// Discord 接続後に自分の ID を通知する用途のみで、query 系の Store とは分離する。
type BotRegistrar interface {
	AddBotID(platformUserID string)
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
