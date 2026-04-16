package memento

import "context"

// RawMessage は LLM に送信するメッセージの domain 表現。
type RawMessage struct {
	Role    string
	Content string
}

// CompletionResponse は LLM 補完結果の domain 表現。
type CompletionResponse struct {
	Text string
}

// Completer は LLM 補完呼び出しを抽象化するインターフェース。
type Completer interface {
	CompleteRaw(ctx context.Context, msgs []RawMessage) (*CompletionResponse, error)
}

// ConversationMessage は会話メッセージの domain 表現。
// メモリ抽出に必要なフィールドのみを含む。
type ConversationMessage struct {
	Role      string
	Content   string
	UserID    string
	UserName  string
	Source    string
	MediaKeys []string
	ImageURLs []string
}
