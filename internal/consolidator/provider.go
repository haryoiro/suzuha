package consolidator

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/samber/do/v2"
)

// Package registers consolidator providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Server, error) {
		llmClient := do.MustInvoke[*llm.Client](i)
		store := do.MustInvoke[*memory.SQLiteStore](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return NewServer(llmClient, store, logger), nil
	})
}
