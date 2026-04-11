package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/feature/location"
)

// LocationProvider は現在地情報をプロンプトブロックとして提供する。
type LocationProvider struct {
	Store *location.Store
}

func (p *LocationProvider) ProvideContext(_ context.Context, _ Request) Block {
	if p.Store == nil {
		return Block{}
	}
	snippet := p.Store.BuildContextSnippet()
	if snippet == "" {
		return Block{}
	}
	return Block{Background: []llm.Message{{
		Role: "system", Content: snippet, Timestamp: jtime.Now(),
	}}}
}
