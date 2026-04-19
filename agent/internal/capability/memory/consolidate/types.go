package consolidate

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	"github.com/haryoiro/suzuha/internal/llm"
)

// Completer はLLM補完呼び出しを抽象化するインターフェース (consumer-side)。
// acquire.Completer と構造は同一だが、サブ package 間で import しないため
// 両 package に個別に定義する (consumer-side interface の定石)。
type Completer interface {
	CompleteRaw(ctx context.Context, msgs []llm.RawMessage) (*llm.Response, error)
}

// ConsolidateOpts / ConsolidateResult は domain/memo への型エイリアス。
// consolidate サブ package 内部のシグネチャで短縮名を使うため。
// forget サブ package は capability sibling 経由を避けて直接 domain/memo を
// import する。
type (
	ConsolidateOpts   = memo.ConsolidateOpts
	ConsolidateResult = memo.ConsolidateResult
)
