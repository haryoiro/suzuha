package prompt

import (
	"context"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/llm"
)

// LocationSnippetBuilder は位置情報のコンテキストスニペットを生成する (consumer-side interface)。
type LocationSnippetBuilder interface {
	BuildContextSnippet() string
}

// LocationProvider は位置情報をコンテキストに注入する。
type LocationProvider struct {
	Store LocationSnippetBuilder
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
