// Package user は内部ユーザーの Store 契約を定義する。
// 実装は internal/user/store.go (Phase 5 で adapter/store/user/ に移動予定)、
// データ型は domain/user/。
package user

import (
	"context"

	domain "github.com/haryoiro/suzuha/internal/domain/user"
)

// BotRegistrar は起動後に判明する bot の platform user ID を登録する。
// Discord 接続後に自分の ID を通知する用途のみで、query 系の Store とは分離する。
type BotRegistrar interface {
	AddBotID(platformUserID string)
}

// Store はユーザーストレージの主 interface。
type Store interface {
	// Resolve は platform + platform_user_id で内部ユーザーを検索する。
	// 存在しなければ自動作成してリンクを張る。CLI platform のユーザーは RoleOwner で作る。
	Resolve(ctx context.Context, platform, platformUserID, platformName string) (*domain.User, error)

	// Get は内部 ID でユーザーを返す。
	Get(ctx context.Context, id string) (*domain.User, error)

	// UpdateDisplayName はニックネームを変更する。
	UpdateDisplayName(ctx context.Context, userID, displayName string) error

	// TrackGuildChannel はユーザーが guild + channel で見られたことを記録する。
	TrackGuildChannel(ctx context.Context, userID, guildID, guildName, channelID, channelName string) error

	// GetUserGuilds はユーザーが見られた guild と channel を返す。
	GetUserGuilds(ctx context.Context, userID string) ([]domain.UserGuild, error)

	// ResolveExisting は存在チェックのみ。見つからなければ sql.ErrNoRows をラップして返す。
	ResolveExisting(ctx context.Context, platform, platformUserID string) (*domain.User, error)

	// ListMentionable は discord platform link を持つ non-bot ユーザーを返す。mention 対象選定用。
	ListMentionable(ctx context.Context) ([]domain.MentionableUser, error)

	// Close はリソースを解放する。
	Close() error
}

// AdminStore は管理画面が必要とする追加操作を含む。
type AdminStore interface {
	Store

	// List はページネーション付きでユーザー一覧を返す。
	List(ctx context.Context, offset, limit int) ([]domain.User, int, error)

	// Update は部分更新を適用する (nil フィールドは更新しない)。
	Update(ctx context.Context, id string, fields domain.UpdateFields) error

	// ListPlatformLinks はユーザーの全 platform link を返す。
	ListPlatformLinks(ctx context.Context, userID string) ([]domain.PlatformLink, error)

	// ListGuilds は全 guild をメンバー数/チャンネル数付きで返す。
	ListGuilds(ctx context.Context) ([]domain.GuildSummary, error)

	// ListAllChannels は guild 情報を含む全 channel を返す。
	ListAllChannels(ctx context.Context) ([]domain.ChannelEntry, error)

	// GetGuildChannels は指定 guild の channel 一覧をアクティビティ情報付きで返す。
	GetGuildChannels(ctx context.Context, guildID string) ([]domain.GuildChannel, error)
}
