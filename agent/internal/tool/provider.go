package tool

import "github.com/samber/do/v2"

// Package registers tool registry providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Registry, error) {
		return NewRegistry(), nil
	})
}
