package memory

import (
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers memory store providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*SQLiteStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		logger := do.MustInvoke[*slog.Logger](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return NewSQLiteStore(cfg.Memory.DBPath, embedder, true, logger)
	})
}
