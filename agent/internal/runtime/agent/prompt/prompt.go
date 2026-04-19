package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/message"
)

// Request は LLM への会話リクエストに必要な情報をまとめる。
type Request struct {
	Query        string
	ImageURLs    []string
	Source       string
	EventType    string
	Channel      string
	BotID        string
	Messages     []message.Message
	Participants []Participant
	// self-prompt など、Foreground に載せるテキスト
	EventContent string
	IsHome       bool
}

// Participant は会話の参加者情報を保持する。
type Participant struct {
	Platform string
	UserID   string
}

// Block はプロンプトの背景情報とフォアグラウンド情報をまとめる。
type Block struct {
	Background []message.Message
	Foreground []message.Message
}

// Provider はコンテキスト情報をプロンプトブロックとして提供する。
type Provider interface {
	ProvideContext(ctx context.Context, req Request) Block
}
