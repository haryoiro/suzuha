package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/message"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/runtime/event"
)

// SelfPromptProvider はセルフプロンプトイベントの内容をプロンプトブロックとして提供する。
type SelfPromptProvider struct{}

func (SelfPromptProvider) ProvideContext(_ context.Context, req Request) Block {
	if req.EventType != event.TypeSelfPrompt {
		return Block{}
	}
	return Block{Foreground: []message.Message{{
		Role: "system", Content: req.EventContent, Timestamp: jtime.Now(),
	}}}
}
