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

// Replier is an optional interface for platforms that support message replies.
type Replier interface {
	// SendReply sends a message as a reply to replyToID.
	// Returns the platform message ID of the sent message.
	SendReply(ctx context.Context, channel, text, replyToID string) (string, error)
}

// IDSender is an optional interface for platforms that can return message IDs.
type IDSender interface {
	// SendWithID sends a message and returns its platform message ID.
	SendWithID(ctx context.Context, channel, text string) (string, error)
}

// Typer is an optional interface for platforms that support typing indicators.
type Typer interface {
	// Typing sends a typing indicator to the specified channel.
	Typing(ctx context.Context, channel string)
}

// VoiceSpeaker is an optional interface for platforms that support voice output.
type VoiceSpeaker interface {
	// SpeakText synthesizes and sends voice audio to the channel's guild voice connection.
	SpeakText(ctx context.Context, guildID, text string) error

	// IsConnected returns true if there is an active voice session for the guild.
	IsConnected(guildID string) bool
}
