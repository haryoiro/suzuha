package agent

import "time"

// SourceKey identifies an interaction source.
type SourceKey string

const (
	SourceKeyDiscord SourceKey = "discord"
	SourceKeyDevice  SourceKey = "device"
)

// DirectiveConfig holds source-specific pipeline settings.
type DirectiveConfig struct {
	// ForceRespond が true の場合、skip_response を使わず必ず応答する
	ForceRespond bool
	// DrainWindow はイベントバッチの待ち時間 (0 = 即時処理)
	DrainWindow time.Duration
	// DirectiveTemplate はソース固有の directive テンプレート
	// 空の場合はパイプラインのデフォルト (conversationState ベース) を使う
	DirectiveTemplate string
	// SkipChannelFilter が true の場合、チャンネル設定フィルタをスキップ
	SkipChannelFilter bool
	// SkipCatchUpStale が true の場合、stale batch catch-up をスキップ
	SkipCatchUpStale bool
	// SkipChannelHistory が true の場合、チャンネル履歴注入をスキップ
	SkipChannelHistory bool
}

// discordDirectiveConfig returns the DirectiveConfig for Discord sources.
func discordDirectiveConfig(drainWindow time.Duration) DirectiveConfig {
	return DirectiveConfig{
		DrainWindow: drainWindow,
	}
}

// deviceDirectiveConfig returns the DirectiveConfig for physical device sources.
func deviceDirectiveConfig() DirectiveConfig {
	return DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        0,
		DirectiveTemplate:  "[RESPOND] 物理デバイス経由の音声対話です。必ず返答してください。話し言葉で自然に返して。1〜2文で短く。skip_response は使わないで。テキストに絵文字・顔文字は入れない。音声で読まれるので句読点や記号は控えめに。",
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: true,
	}
}

// sourceKeyForEvent maps an event source string to a SourceKey.
func sourceKeyForEvent(source string) SourceKey {
	switch source {
	case "device":
		return SourceKeyDevice
	default:
		return SourceKeyDiscord
	}
}
