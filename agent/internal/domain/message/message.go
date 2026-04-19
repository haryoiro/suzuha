// Package message は agent パイプラインの中立メッセージ型を定義する。
//
// target-layout.md §3 に従い「複数 package から参照される Entity / Value
// Object」として domain に配置する。capability/llm の Client 実装や
// runtime/agent の pipeline が共通に使う形。
package message

import (
	"time"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// Message は suzuha 内部のメッセージ形式。チャンネル / ユーザー情報を保持する。
type Message struct {
	Role        string    `json:"role"` // "user", "assistant", "system", "tool"
	Content     string    `json:"content"`
	UserID      string    `json:"user_id,omitempty"`
	UserName    string    `json:"user_name,omitempty"`
	Source      string    `json:"source,omitempty"` // "discord", "cli"
	Channel     string    `json:"channel,omitempty"`
	ChannelName string    `json:"channel_name,omitempty"`
	GuildID     string    `json:"guild_id,omitempty"`
	GuildName   string    `json:"guild_name,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	ToolCallID  string    `json:"tool_call_id,omitempty"`

	// ToolCalls は assistant がツール呼び出しを要求する場合に設定される。
	ToolCalls []providers.ToolCall `json:"tool_calls,omitempty"`

	// ImageURLs はこのメッセージに添付された画像 URL。
	// vision capable な LLM の場合はマルチモーダル content parts として送られる。
	ImageURLs []string `json:"image_urls,omitempty"`

	// MediaKeys は永続化された media attachment の MediaStore キー。
	// consolidator が抽出された記憶に media を紐付けるために使う。
	MediaKeys []string `json:"media_keys,omitempty"`

	// Injected はこのメッセージがチャンネル履歴から注入された過去発言である
	// ことを示す。保存 (SaveSnapshot), 会話ログ (logConversationTurn),
	// 記憶抽出 (acquirer), Think ステート計算の対象外とする。
	Injected bool `json:"injected,omitempty"`
}
