package observe

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers observability providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*RingBuffer, error) {
		return NewRingBuffer(1000), nil
	})
	do.Provide(i, func(i do.Injector) (*slog.Logger, error) {
		cfg := do.MustInvoke[*config.Config](i)
		ring := do.MustInvoke[*RingBuffer](i)
		return NewLoggerWithRing(cfg.Observe.LogLevel, ring), nil
	})
}
