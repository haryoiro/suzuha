// Package chat は chat プラットフォーム (Discord / CLI 等) の契約 interface を定義する。
// 実装は internal/adapter/{cli,discord}/、高レイヤでは port/chat 越しのみ参照する。
package chat

import "context"

// Sender はテキストメッセージ送信の基本インターフェース。
// Session や Notifier など、送信機能だけが必要な consumer はこちらを使う。
type Sender interface {
	Send(ctx context.Context, channel string, text string) error
}

// Interface はチャットプラットフォームのライフサイクル + 送信の複合インターフェース。
// 新規コードでは gateway.Source (ライフサイクル) と chat.Sender (送信) を個別に使うこと。
type Interface interface {
	Sender
	Run(ctx context.Context) error
}

// Replier はメッセージ返信をサポートするプラットフォーム用のオプショナル interface。
type Replier interface {
	// SendReply は replyToID への返信としてメッセージを送る。送信した platform message ID を返す。
	SendReply(ctx context.Context, channel, text, replyToID string) (string, error)
}

// IDSender は message ID を返せるプラットフォーム用のオプショナル interface。
type IDSender interface {
	// SendWithID はメッセージを送信して platform message ID を返す。
	SendWithID(ctx context.Context, channel, text string) (string, error)
}

// Typer はタイピングインジケータをサポートするプラットフォーム用のオプショナル interface。
type Typer interface {
	// Typing は指定チャンネルにタイピング中状態を送る。
	Typing(ctx context.Context, channel string)
}

// VoiceSpeaker は音声出力をサポートするプラットフォーム用のオプショナル interface。
type VoiceSpeaker interface {
	// SpeakText は text を合成してチャンネルの guild voice に送る。
	SpeakText(ctx context.Context, guildID, text string) error

	// SpeakStream は文チャネルから逐次 TTS → 音声送信する。
	// チャネルがクローズされるまで各文を TTS で合成し、ストリーミングで送信する。
	SpeakStream(ctx context.Context, guildID string, sentences <-chan string) error

	// IsConnected は guild にアクティブな voice session があれば true を返す。
	IsConnected(guildID string) bool
}
