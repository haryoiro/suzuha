package stt

import (
	"context"
	"fmt"
	"log/slog"
)

// STT transcribes audio to text.
type STT interface {
	Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}

// STTProviderConfig holds the configuration for a single STT provider.
type STTProviderConfig struct {
	Provider string `yaml:"provider"` // "deepgram", "whispercpp"
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
	URL      string `yaml:"url"`
}

// NewSTT creates an STT client from a single provider config.
func NewSTT(cfg STTProviderConfig) (STT, error) {
	switch cfg.Provider {
	case "deepgram":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("stt: deepgram requires api_key")
		}
		return NewDeepgram(cfg.APIKey, cfg.Model), nil
	case "whispercpp":
		if cfg.URL == "" {
			return nil, fmt.Errorf("stt: whispercpp requires url")
		}
		return NewWhisper(cfg.URL), nil
	default:
		return nil, fmt.Errorf("stt: unknown provider %q", cfg.Provider)
	}
}

// NewSTTChain creates an STT chain from a list of provider configs (first = highest priority).
// If a higher-priority provider fails at runtime, the next one is tried.
func NewSTTChain(configs []STTProviderConfig, logger *slog.Logger) (STT, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("stt: no providers configured")
	}
	var clients []STT
	for _, cfg := range configs {
		c, err := NewSTT(cfg)
		if err != nil {
			logger.Warn("音声認識の設定をスキップ", "provider", cfg.Provider, "error", err)
			continue
		}
		logger.Info("音声認識を準備した", "provider", cfg.Provider)
		clients = append(clients, c)
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("stt: no valid providers")
	}
	if len(clients) == 1 {
		return clients[0], nil
	}
	return &chainSTT{clients: clients, logger: logger}, nil
}

// chainSTT tries multiple STT providers in order, falling back on error.
type chainSTT struct {
	clients []STT
	logger  *slog.Logger
}

func (c *chainSTT) Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error) {
	var lastErr error
	for _, client := range c.clients {
		text, err := client.Transcribe(ctx, pcm, sampleRate)
		if err == nil {
			return text, nil
		}
		lastErr = err
		c.logger.Warn("別の音声認識で再試行", "error", err)
	}
	return "", fmt.Errorf("stt: all providers failed: %w", lastErr)
}
