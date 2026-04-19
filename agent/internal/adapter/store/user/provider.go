package user

import (
	"database/sql"

	"github.com/haryoiro/suzuha/internal/config"
	port "github.com/haryoiro/suzuha/internal/port/user"
	"github.com/samber/do/v2"
)

// Package registers user store providers into the DI injector.
// port/user.Store / AdminStore / BotRegistrar 契約を DBStore が満たす形で公開する。
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*DBStore, error) {
		db := do.MustInvokeNamed[*sql.DB](i, "shared-db")
		cfg := do.MustInvoke[*config.Config](i)
		return NewDBStore(db, cfg.Discord.BotID), nil
	})

	do.Provide(i, func(i do.Injector) (port.Store, error) {
		return do.MustInvoke[*DBStore](i), nil
	})

	do.Provide(i, func(i do.Injector) (port.AdminStore, error) {
		return do.MustInvoke[*DBStore](i), nil
	})

	do.Provide(i, func(i do.Injector) (port.BotRegistrar, error) {
		return do.MustInvoke[*DBStore](i), nil
	})
}
