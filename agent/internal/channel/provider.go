package channel

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/samber/do/v2"
)

// Package registers channel store providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Store, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		logger := do.MustInvoke[*slog.Logger](i)
		s := NewStore(db)
		if err := s.Reload(context.Background()); err != nil {
			logger.Warn("channel settings reload failed", "error", err)
		}
		return s, nil
	})
	do.Provide(i, func(i do.Injector) (*PostgresActivityStore, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		return NewActivityStore(db), nil
	})
}
