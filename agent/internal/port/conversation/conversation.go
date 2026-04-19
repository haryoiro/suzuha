package conversation

import (
	"context"
	"time"

	domainchannel "github.com/haryoiro/suzuha/internal/domain/channel"
)

// SettingsStore はチャンネル設定の読み書きを抽象化する契約。
type SettingsStore interface {
	Get(channelID string) domainchannel.Settings
	GetMode(channelID string) domainchannel.Mode
	Set(ctx context.Context, cs *domainchannel.Settings) error
	Delete(ctx context.Context, channelID string) error
	List(ctx context.Context, guildID string) ([]domainchannel.Settings, error)
	HomeChannelID() string
	Reload(ctx context.Context) error
}

// ActivityStore はチャンネル活動データへの read アクセスを提供する契約。
type ActivityStore interface {
	LastInteractionGlobal(ctx context.Context) (lastMsg time.Time, channelID string, err error)
}
