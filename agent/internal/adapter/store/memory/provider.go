package memory

import (
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/adapter/embedder"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package は DBStore (concrete) と interface bridging (Backend) を DI に登録する。
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*DBStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		if cfg.Memory.PostgresURL == "" {
			return nil, fmt.Errorf("memory: postgres_url が設定されていません")
		}
		logger := do.MustInvoke[*slog.Logger](i)
		embedderClient := do.MustInvokeNamed[embedding.Embedder](i, "embedder")
		return NewDBStore(cfg.Memory.PostgresURL, embedderClient, true, logger)
	})

	do.Provide(i, func(i do.Injector) (Backend, error) {
		return do.MustInvoke[*DBStore](i), nil
	})
}
