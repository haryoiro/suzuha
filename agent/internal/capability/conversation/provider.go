package conversation

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/samber/do/v2"
)

// Package は conversation capability の DI を登録する。
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*SettingsStore, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		logger := do.MustInvoke[*slog.Logger](i)
		s := NewSettingsStore(db)
		if err := s.Reload(context.Background()); err != nil {
			logger.Warn("channel settings reload failed", "error", err)
		}
		return s, nil
	})
	do.Provide(i, func(i do.Injector) (*DBActivityStore, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		return NewActivityStore(db), nil
	})
}
