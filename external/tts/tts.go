package tts

import (
	"context"
	"fmt"
	"log/slog"
)

// TTS synthesizes text to PCM audio.
type TTS interface {
	// Synthesize converts text to raw PCM audio (16-bit LE, mono).
	// Returns the PCM data and the output sample rate.
	Synthesize(ctx context.Context, text string) (pcm []byte, sampleRate int, err error)
}

// TTSProviderConfig holds the configuration for a single TTS provider.
type TTSProviderConfig struct {
	Provider  string `yaml:"provider"`   // "voicevox", "sbv2"
	URL       string `yaml:"url"`        // Server URL
	SpeakerID int    `yaml:"speaker_id"` // VOICEVOX speaker ID
	Model     string `yaml:"model"`      // SBV2 model name
	Style     string `yaml:"style"`      // SBV2 style name
}

// NewTTS creates a TTS client from a single provider config.
func NewTTS(cfg TTSProviderConfig) (TTS, error) {
	switch cfg.Provider {
	case "voicevox":
		if cfg.URL == "" {
			return nil, fmt.Errorf("tts: voicevox requires url")
		}
		return NewVoicevox(cfg.URL, cfg.SpeakerID), nil
	case "sbv2":
		if cfg.URL == "" {
			return nil, fmt.Errorf("tts: sbv2 requires url")
		}
		return NewSBV2(cfg.URL, cfg.Model, cfg.Style), nil
	default:
		return nil, fmt.Errorf("tts: unknown provider %q", cfg.Provider)
	}
}

// NewTTSChain creates a TTS chain from a list of provider configs (first = highest priority).
// If a higher-priority provider fails at runtime, the next one is tried.
func NewTTSChain(configs []TTSProviderConfig, logger *slog.Logger) (TTS, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("tts: no providers configured")
	}
	var clients []TTS
	for _, cfg := range configs {
		c, err := NewTTS(cfg)
		if err != nil {
			logger.Warn("声の合成の設定をスキップ", "provider", cfg.Provider, "error", err)
			continue
		}
		logger.Info("声の合成を準備した", "provider", cfg.Provider)
		clients = append(clients, c)
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("tts: no valid providers")
	}
	if len(clients) == 1 {
		return clients[0], nil
	}
	return &chainTTS{clients: clients, logger: logger}, nil
}

// chainTTS tries multiple TTS providers in order, falling back on error.
type chainTTS struct {
	clients []TTS
	logger  *slog.Logger
}

func (c *chainTTS) Synthesize(ctx context.Context, text string) ([]byte, int, error) {
	var lastErr error
	for _, client := range c.clients {
		pcm, sr, err := client.Synthesize(ctx, text)
		if err == nil {
			return pcm, sr, nil
		}
		lastErr = err
		c.logger.Warn("別の声で再試行", "error", err)
	}
	return nil, 0, fmt.Errorf("tts: all providers failed: %w", lastErr)
}
