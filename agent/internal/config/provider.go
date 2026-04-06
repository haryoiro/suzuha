package config

import "github.com/samber/do/v2"

// Package registers config providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Config, error) {
		path := do.MustInvokeNamed[string](i, "config-path")
		return Load(path)
	})
}
