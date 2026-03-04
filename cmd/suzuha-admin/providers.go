package main

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/admin"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/haryoiro/suzuha/internal/user"
	"github.com/samber/do/v2"
)

// adminPackages returns all DI package functions for the admin process.
func adminPackages(cfgPath string) []func(do.Injector) {
	return []func(do.Injector){
		config.Package,
		func(i do.Injector) {
			do.ProvideNamedValue(i, "config-path", cfgPath)

			// Logger (no ring buffer for admin).
			do.Provide(i, func(i do.Injector) (*slog.Logger, error) {
				cfg := do.MustInvoke[*config.Config](i)
				return observe.NewLogger(cfg.Observe.LogLevel), nil
			})

			// Memory store (read-write, no migrations, no embed).
			do.Provide(i, func(i do.Injector) (*memory.SQLiteStore, error) {
				cfg := do.MustInvoke[*config.Config](i)
				logger := do.MustInvoke[*slog.Logger](i)
				return memory.NewSQLiteStore(cfg.Memory.DBPath, nil, false, logger)
			})

			// User store (no bot ID for admin).
			do.Provide(i, func(i do.Injector) (*user.SQLiteStore, error) {
				store := do.MustInvoke[*memory.SQLiteStore](i)
				return user.NewSQLiteStore(store.DB()), nil
			})

			// Schedule store.
			do.Provide(i, func(i do.Injector) (*schedule.Store, error) {
				store := do.MustInvoke[*memory.SQLiteStore](i)
				return schedule.NewStore(store.DB()), nil
			})

			// Admin server.
			do.Provide(i, func(i do.Injector) (*admin.Server, error) {
				cfg := do.MustInvoke[*config.Config](i)
				store := do.MustInvoke[*memory.SQLiteStore](i)
				userStore := do.MustInvoke[*user.SQLiteStore](i)
				schedStore := do.MustInvoke[*schedule.Store](i)
				logger := do.MustInvoke[*slog.Logger](i)
				return admin.NewServer(cfg.Admin, store, userStore, schedStore, logger), nil
			})
		},
	}
}
