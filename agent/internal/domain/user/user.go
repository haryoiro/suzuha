// Package user はユーザー関連のドメイン型を定義する。
// Store / AdminStore / BotRegistrar の interface は internal/user/ にあり、
// 本 package は純データ型のみを提供する。
package user

import "time"

// Role はユーザーの権限レベル。
type Role string

// Role 定数は既定の権限レベル。
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

// User は suzuha 内部のユーザー識別。
type User struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        Role           `json:"role"`
	IsBot       bool           `json:"is_bot"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// PlatformLink は内部ユーザーと外部プラットフォーム (Discord 等) の紐付け。
type PlatformLink struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	PlatformName   string    `json:"platform_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// UserGuild はユーザーが所属するギルド + チャンネルの組。
type UserGuild struct {
	GuildID     string    `json:"guild_id"`
	GuildName   string    `json:"guild_name"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// UpdateFields は admin Update の部分更新フィールド。nil は「更新しない」。
type UpdateFields struct {
	DisplayName *string
	Role        *Role
	IsBot       *bool
}

// MentionableUser はメンション対象の軽量リードモデル。
type MentionableUser struct {
	DisplayName   string `json:"display_name"`
	DiscordUserID string `json:"discord_user_id"`
}

// GuildSummary は admin のギルド一覧用リードモデル。
type GuildSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	UpdatedAt    time.Time `json:"updated_at"`
	MemberCount  int       `json:"member_count"`
	ChannelCount int       `json:"channel_count"`
}

// ChannelEntry はチャンネル + ギルドのフラットなレコード。
type ChannelEntry struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	GuildID     string `json:"guild_id"`
	GuildName   string `json:"guild_name"`
}

// GuildChannel はチャンネルデータとアクティビティ情報の組。
type GuildChannel struct {
	ChannelID         string  `json:"channel_id"`
	ChannelName       string  `json:"channel_name"`
	UserCount         int     `json:"user_count"`
	LastSeenAt        string  `json:"last_seen_at"`
	LastUserMessageAt *string `json:"last_user_message_at,omitempty"`
}
