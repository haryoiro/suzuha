package memory

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers memory store providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*SQLiteStore, error) {
		cfg := do.MustInvoke[*config.Config](i)
		logger := do.MustInvoke[*slog.Logger](i)
		embedFn := do.MustInvokeNamed[EmbedFunc](i, "embed-func")
		return NewSQLiteStore(cfg.Memory.DBPath, embedFn, true, logger)
	})
}
