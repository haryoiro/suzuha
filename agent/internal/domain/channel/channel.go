package channel

import "time"

// Mode はチャンネルにおける bot の挙動を表す。
type Mode string

const (
	// ModeActive はメッセージを読み取り応答する通常モード (既定値)。
	ModeActive Mode = "active"
	// ModeListen はメッセージを取り込むが応答しないモード。
	ModeListen Mode = "listen"
	// ModeDisabled はチャンネルを完全に無視するモード。
	ModeDisabled Mode = "disabled"
)

// Settings はチャンネル単位の設定を表す値オブジェクト。
type Settings struct {
	ChannelID string    `json:"channel_id"`
	GuildID   string    `json:"guild_id"`
	Mode      Mode      `json:"mode"`
	Home      bool      `json:"home"`
	UpdatedAt time.Time `json:"updated_at"`
}
