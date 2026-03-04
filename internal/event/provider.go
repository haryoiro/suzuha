package event

import "github.com/samber/do/v2"

// Package registers event bus providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Bus, error) {
		return NewBus(128), nil
	})
}
