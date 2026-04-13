package memory

import (
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/samber/do/v2"
)

// Package registers memory store providers into the DI injector.
func Package(i do.Injector) {
	// Backend は SQLiteStore か PostgresStore のいずれかを提供する。
	do.Provide(i, func(i do.Injector) (Backend, error) {
		cfg := do.MustInvoke[*config.Config](i)
		logger := do.MustInvoke[*slog.Logger](i)
		clock := do.MustInvoke[*jtime.Clock](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")

		if cfg.Memory.PostgresURL != "" {
			return NewPostgresStore(cfg.Memory.PostgresURL, clock, embedder, true, logger)
		}
		return newSQLiteBackend(cfg.Memory.DBPath, embedder, logger)
	})
}
