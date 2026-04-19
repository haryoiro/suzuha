// Package user の正準定義は port/user (interface) + domain/user (型) にある。
// 本 file は呼び出し側の import path 温存のための互換 shim。
// Phase 5 で adapter/store/user/ に分解したタイミングで本 shim は不要になる。
package user

import (
	domain "github.com/haryoiro/suzuha/internal/domain/user"
	port "github.com/haryoiro/suzuha/internal/port/user"
)

// domain/user への型エイリアス群 (データ型)。
type (
	Role            = domain.Role
	User            = domain.User
	PlatformLink    = domain.PlatformLink
	UserGuild       = domain.UserGuild
	UpdateFields    = domain.UpdateFields
	MentionableUser = domain.MentionableUser
	GuildSummary    = domain.GuildSummary
	ChannelEntry    = domain.ChannelEntry
	GuildChannel    = domain.GuildChannel
)

// port/user への interface エイリアス群 (契約)。
type (
	BotRegistrar = port.BotRegistrar
	Store        = port.Store
	AdminStore   = port.AdminStore
)

// Role 定数は domain/user の値を再エクスポート。
const (
	RoleOwner  = domain.RoleOwner
	RoleMember = domain.RoleMember
	RoleGuest  = domain.RoleGuest
)
