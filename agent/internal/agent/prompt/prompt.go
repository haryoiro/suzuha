package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/llm"
)

type Request struct {
	Query        string
	ImageURLs    []string
	Source       string
	EventType    string
	Channel      string
	BotID        string
	Messages     []llm.Message
	Participants []Participant
	// self-prompt など、Foreground に載せるテキスト
	EventContent string
	IsHome       bool
}

type Participant struct {
	Platform string
	UserID   string
}

type Block struct {
	Background []llm.Message
	Foreground []llm.Message
}

type Provider interface {
	ProvideContext(ctx context.Context, req Request) Block
}
