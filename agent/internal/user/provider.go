package user

import (
	"database/sql"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers user store providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*PostgresStore, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		cfg := do.MustInvoke[*config.Config](i)
		return NewPostgresStore(db, cfg.Discord.BotID), nil
	})
}
