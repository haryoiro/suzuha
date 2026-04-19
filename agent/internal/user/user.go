// Package user は domain/user (型) + port/user (interface) + adapter/store/user
// (実装) への互換 shim。既存呼び出し側の `user.X` 参照を温存するため、
// type alias と re-export を集約する。callers を正準 package に移した時点で
// 本 package ごと廃止予定。
package user

import (
	"database/sql"

	adapterUser "github.com/haryoiro/suzuha/internal/adapter/store/user"
	domain "github.com/haryoiro/suzuha/internal/domain/user"
	port "github.com/haryoiro/suzuha/internal/port/user"
	"github.com/samber/do/v2"
)

// domain/user への型エイリアス (データ型)。
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

// port/user への interface エイリアス (契約)。
type (
	BotRegistrar = port.BotRegistrar
	Store        = port.Store
	AdminStore   = port.AdminStore
)

// adapter/store/user.DBStore の型エイリアス。
type DBStore = adapterUser.DBStore

// Role 定数は domain/user の値を再エクスポート。
const (
	RoleOwner  = domain.RoleOwner
	RoleMember = domain.RoleMember
	RoleGuest  = domain.RoleGuest
)

// NewDBStore は adapter/store/user.NewDBStore の再エクスポート。
func NewDBStore(db *sql.DB, botPlatformUserIDs ...string) *DBStore {
	return adapterUser.NewDBStore(db, botPlatformUserIDs...)
}

// Package は adapter/store/user.Package の再エクスポート (DI 登録)。
func Package(i do.Injector) {
	adapterUser.Package(i)
}
