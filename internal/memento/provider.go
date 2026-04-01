package memento

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/samber/do/v2"
)

// Package registers memento providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Acquirer, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[*memory.SQLiteStore](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return NewAcquirer(llmClient, store, DefaultAcquireConfig(), logger), nil
	})

	do.Provide(i, func(i do.Injector) (*Consolidator, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[*memory.SQLiteStore](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return NewConsolidator(llmClient, store, store, logger), nil
	})
}
