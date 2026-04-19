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
	do.Provide(i, func(i do.Injector) (*PostgresStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		if cfg.Memory.PostgresURL == "" {
			return nil, fmt.Errorf("memory: postgres_url が設定されていません")
		}
		logger := do.MustInvoke[*slog.Logger](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return NewPostgresStore(cfg.Memory.PostgresURL, embedder, true, logger)
	})

	do.Provide(i, func(i do.Injector) (Backend, error) {
		return do.MustInvoke[*PostgresStore](i), nil
	})
}
