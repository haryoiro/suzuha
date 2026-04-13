package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/feature/location"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

type LocationProvider struct {
	Clock *jtime.Clock
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
		Role: "system", Content: snippet, Timestamp: p.Clock.Now(),
	}}}
}
