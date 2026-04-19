package agent

import (
	"context"
	"time"
)

// SourceKey identifies an interaction source.
type SourceKey string

const (
	SourceKeyDiscord SourceKey = "discord"
	SourceKeyDevice  SourceKey = "device"
	SourceKeyWeb     SourceKey = "web"
	SourceKeyCLI     SourceKey = "cli"
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

// DiscordDirectiveConfig returns the DirectiveConfig for Discord sources.
// channel/discord.Session から呼び出される (exported)。
func DiscordDirectiveConfig(drainWindow time.Duration) DirectiveConfig {
	return DirectiveConfig{
		DrainWindow: drainWindow,
	}
}

// DeviceDirectiveConfig returns the DirectiveConfig for physical device sources.
// channel/device.Session から呼び出される (exported)。
func DeviceDirectiveConfig() DirectiveConfig {
	return DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        2 * time.Second,
		DirectiveTemplate:  "[RESPOND] 物理デバイス経由の音声対話です。必ず返答してください。話し言葉で自然に返して。1〜2文で短く。skip_response は使わないで。テキストに絵文字・顔文字は入れない。音声で読まれるので句読点や記号は控えめに。",
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: true,
	}
}

// StreamingSession はストリーミング TTS に対応したセッション用の拡張 interface。
// actStreamWith がこれを満たすセッションに対して sentence 単位のストリーミング応答を行う。
type StreamingSession interface {
	Session
	// RespondStream は LLM がストリームで生成した sentence を逐次読み上げる。
	// sentences チャネルは呼び出し側で close される。
	RespondStream(ctx context.Context, sentences <-chan string) error
	// IsVoiceReady はこのターンで実際に voice 出力できる状態かを返す
	// (VC に接続中か、websocket クライアントがつないでいるか、等)。
	IsVoiceReady() bool
}

// Session represents a single interaction session.
// Each source (Discord, Device, CLI) implements this interface.
type Session interface {
	// Source returns this session's source identifier.
	Source() SourceKey
	// Context returns this session's conversation context (message history).
	Context() *Context
	// DirectiveConfig returns source-specific pipeline settings.
	DirectiveConfig() DirectiveConfig
	// PersistKey returns the key used for context persistence in the database.
	PersistKey() string
	// BeginTurn sets the routing context for the current conversation turn.
	// Called after Perceive, before Think/Act.
	BeginTurn(p *Perception)
	// Respond sends response text through this session's output.
	Respond(ctx context.Context, text string) error
}

// WebDirectiveConfig returns the DirectiveConfig for web widget sources.
// channel/web.Session から呼び出される (exported)。
func WebDirectiveConfig() DirectiveConfig {
	return DirectiveConfig{
		ForceRespond:       true,
		DrainWindow:        2 * time.Second,
		DirectiveTemplate:  "[RESPOND] Webウィジェット経由の音声対話です。必ず返答してください。話し言葉で自然に返して。1〜2文で短く。skip_response は使わないで。テキストに絵文字・顔文字は入れない。音声で読まれるので句読点や記号は控えめに。",
		SkipChannelFilter:  true,
		SkipCatchUpStale:   true,
		SkipChannelHistory: true,
	}
}

// typingSession はタイピングインジケータをサポートする Session 用のオプショナル interface。
// act ループが tool 実行中に typing を出すのに使う (Discord session が満たす)。
type typingSession interface {
	Typing(ctx context.Context)
}

// sourceKeyForEvent maps an event source string to a SourceKey.
func sourceKeyForEvent(source string) SourceKey {
	switch source {
	case "device":
		return SourceKeyDevice
	case "web":
		return SourceKeyWeb
	case "cli":
		return SourceKeyCLI
	default:
		return SourceKeyDiscord
	}
}
