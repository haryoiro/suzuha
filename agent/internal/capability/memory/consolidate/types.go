package consolidate

import (
	"github.com/haryoiro/suzuha/internal/domain/memo"
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// Completer は port/llm.Completer の型エイリアス。
type Completer = portllm.Completer

// ConsolidateOpts / ConsolidateResult は domain/memo への型エイリアス。
type (
	ConsolidateOpts   = memo.ConsolidateOpts
	ConsolidateResult = memo.ConsolidateResult
)
