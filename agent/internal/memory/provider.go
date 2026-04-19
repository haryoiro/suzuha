package memory

import (
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/adapter/embedder"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers memory store providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*DBStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		if cfg.Memory.PostgresURL == "" {
			return nil, fmt.Errorf("memory: postgres_url が設定されていません")
		}
		logger := do.MustInvoke[*slog.Logger](i)
		embedder := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return NewDBStore(cfg.Memory.PostgresURL, embedder, true, logger)
	})

	do.Provide(i, func(i do.Injector) (Backend, error) {
		return do.MustInvoke[*DBStore](i), nil
	})
}
