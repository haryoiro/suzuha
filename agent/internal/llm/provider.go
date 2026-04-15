package llm

import (
	"log/slog"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/samber/do/v2"
)

// Package registers LLM client providers into the DI injector.
func Package(i do.Injector) {
	do.Provide(i, func(i do.Injector) (*Client, error) {
		cfg := do.MustInvoke[*config.Config](i)
		logger := do.MustInvoke[*slog.Logger](i)
		return NewClient(
			cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase,
			cfg.LLM.MaxTokens,
			EmbeddingConfig{
				Provider: cfg.Embedding.Provider,
				Model:    cfg.Embedding.Model,
				APIKey:   cfg.Embedding.APIKey,
				APIBase:  cfg.Embedding.APIBase,
				Dims:     cfg.Embedding.Dims,
			},
			VisionConfig{
				Provider: cfg.Vision.Provider,
				Model:    cfg.Vision.Model,
				APIKey:   cfg.Vision.APIKey,
				APIBase:  cfg.Vision.APIBase,
			},
			logger,
		)
	})

	do.Provide(i, func(i do.Injector) (*TokenCounterFactory, error) {
		return NewTokenCounterFactory(), nil
	})
}
