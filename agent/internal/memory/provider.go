package memory

import (
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers memory store providers into the DI injector.
func Package(i do.Injector) {
	// PostgresStore を個別に登録 (shared-db 等から直接参照される)。
	do.Provide(i, func(i do.Injector) (*PostgresStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		if cfg.Memory.PostgresURL == "" {
			return nil, fmt.Errorf("memory: postgres_url が設定されていません")
		}
		logger := do.MustInvoke[*slog.Logger](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return NewPostgresStore(cfg.Memory.PostgresURL, embedder, true, logger)
	})

	// Backend は SQLiteStore か PostgresStore のいずれかを提供する。
	do.Provide(i, func(i do.Injector) (Backend, error) {
		cfg := do.MustInvoke[*config.Config](i)
		if cfg.Memory.PostgresURL != "" {
			return do.MustInvoke[*PostgresStore](i), nil
		}
		logger := do.MustInvoke[*slog.Logger](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return newSQLiteBackend(cfg.Memory.DBPath, embedder, logger)
	})
}
