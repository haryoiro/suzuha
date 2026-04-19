package device

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/external/stt"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/voice"
)

// Frame type constants matching firmware/main/config.h.
const (
	FrameAudio   = 0x01 // PCM16 24kHz mono  (Client → Server)
	FrameImage   = 0x02 // JPEG              (ESP32 → Server)
	FrameCommand = 0x03 // JSON              (Server → Client)
	FrameStatus  = 0x04 // JSON              (Client → Server)
	FrameTTS     = 0x05 // PCM 24kHz mono    (Server → Client)
	FrameTTSEnd  = 0x06 // Empty body         (Server → Client): stream end marker
)

// deviceSampleRate is the I2S sample rate on the ESP32-P4 (shared mic/speaker bus).
const deviceSampleRate = 24000

// TTS chunk size: must be < ESP32 WebSocket buffer_size (4096) minus 1 byte frame header.
const ttsChunkSize = 4000

// Speaker is the interface that the agent uses to send TTS audio.
type Speaker interface {
	SpeakText(ctx context.Context, text string) error
}

// ImageHandler processes incoming JPEG frames from the device camera.
// Defined here (consumer-side) so Hub doesn't import vision/detect packages.
type ImageHandler interface {
	HandleImage(jpeg []byte)
}

// Hub manages device and web client connections.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]Client // all connected clients (ESP + Web)
	bus     *event.Bus
	tts     tts.TTS
	stt     stt.STT
	images  ImageHandler
	logger  *slog.Logger

	// VAD for ESP device audio
	espVAD *voice.VAD

	// Owner info (from DB)
	ownerID   string
	ownerName string
}

// NewHub creates a new device Hub.
// Call SetImageHandler after creation to wire the vision pipeline.
func NewHub(bus *event.Bus, ttsClient tts.TTS, sttClient stt.STT, ownerID, ownerName string, logger *slog.Logger) *Hub {
	espVAD := voice.NewVAD()
	espVAD.SpeechThreshold = 100          // match device energy threshold
	espVAD.SilenceDuration = time.Second  // ~10 chunks of 100ms
	espVAD.MinSpeechDuration = 2 * time.Second
	espVAD.MaxSpeechDuration = 10 * time.Second

	return &Hub{
		clients:   make(map[string]Client),
		bus:       bus,
		tts:       ttsClient,
		stt:       sttClient,
		espVAD:    espVAD,
		ownerID:   ownerID,
		ownerName: ownerName,
		logger:    logger,
	}
}

// SetImageHandler sets the handler for incoming camera frames.
func (h *Hub) SetImageHandler(handler ImageHandler) {
	h.images = handler
}

// addClient registers a client in the hub.
func (h *Hub) addClient(c Client) {
	h.mu.Lock()
	h.clients[c.ID()] = c
	total := len(h.clients)
	var web, esp int
	for _, cc := range h.clients {
		if cc.Kind() == "web" {
			web++
		} else if cc.Kind() == "esp" {
			esp++
		}
	}
	h.mu.Unlock()
	h.logger.Info("クライアント接続", "id", c.ID(), "kind", c.Kind(), "total", total, "web", web, "esp", esp)
}

// removeClient unregisters a client from the hub.
func (h *Hub) removeClient(id string) {
	h.mu.Lock()
	delete(h.clients, id)
	total := len(h.clients)
	h.mu.Unlock()
	h.logger.Info("クライアント切断", "id", id, "total", total)
}

// Device returns the first connected ESP device, or nil.
func (h *Hub) Device() *DeviceConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients {
		if dc, ok := c.(*DeviceConn); ok {
			return dc
		}
	}
	return nil
}

// IsConnected returns true if an ESP device is connected.
func (h *Hub) IsConnected() bool {
	return h.Device() != nil
}

// AllClients returns a snapshot of all connected clients.
func (h *Hub) AllClients() []Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Client, 0, len(h.clients))
	for _, c := range h.clients {
		out = append(out, c)
	}
	return out
}

// BroadcastCommandTo sends a JSON command only to clients of the specified kind.
// ESP デバイスと web widget で別々にコマンドを撃ち分けたい場合に使う。
func (h *Hub) BroadcastCommandTo(kind string, cmd map[string]any) error {
	clients := h.AllClients()
	var sent int
	var lastErr error
	for _, c := range clients {
		if c.Kind() != kind {
			continue
		}
		if err := c.SendCommand(cmd); err != nil {
			h.logger.Warn("コマンド送信失敗", "client", c.ID(), "kind", c.Kind(), "error", err)
			lastErr = err
		} else {
			sent++
		}
	}
	if sent == 0 && lastErr == nil {
		return nil // 接続なしは無視
	}
	return lastErr
}

// BroadcastCommand sends a JSON command to all connected clients (ESP + Web).
func (h *Hub) BroadcastCommand(cmd map[string]any) error {
	clients := h.AllClients()
	if len(clients) == 0 {
		return fmt.Errorf("device: クライアント未接続")
	}
	var lastErr error
	for _, c := range clients {
		if err := c.SendCommand(cmd); err != nil {
			h.logger.Warn("コマンド送信失敗", "client", c.ID(), "kind", c.Kind(), "error", err)
			lastErr = err
		}
	}
	return lastErr
}

// SpeakText synthesizes text to PCM via TTS and sends it to all connected clients.
func (h *Hub) SpeakText(ctx context.Context, text string) error {
	clients := h.AllClients()
	if len(clients) == 0 {
		return fmt.Errorf("device: クライアント未接続")
	}
	if h.tts == nil {
		h.logger.Warn("声の合成が設定されていないのでスキップ")
		return nil
	}

	pcm, sampleRate, err := h.tts.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("device: TTS合成失敗: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}

	if sampleRate != deviceSampleRate {
		pcm = voice.ResamplePCM(pcm, sampleRate, deviceSampleRate)
		h.logger.Debug("音声を変換", "from", sampleRate, "to", deviceSampleRate)
		pcm = voice.NormalizePCM(pcm, 20000)
	}

	h.logger.Info("クライアントに声を送った", "pcm_bytes", len(pcm), "clients", len(clients))

	var lastErr error
	for _, c := range clients {
		if err := c.SendTTS(pcm); err != nil {
			h.logger.Warn("TTS送信失敗", "client", c.ID(), "kind", c.Kind(), "error", err)
			lastErr = err
		}
	}
	return lastErr
}

// SpeakTextTo synthesizes text and sends TTS only to clients of the specified kind.
func (h *Hub) SpeakTextTo(ctx context.Context, text string, kind string) error {
	if h.tts == nil {
		h.logger.Warn("声の合成が設定されていないのでスキップ")
		return nil
	}

	// Transcript を先に配信 (web のみ、ログ用)。
	if kind == "web" {
		_ = h.BroadcastCommandTo("web", map[string]any{
			"cmd":  "transcript",
			"kind": "bot",
			"text": text,
		})
	}

	pcm, sampleRate, err := h.tts.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("device: TTS合成失敗: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}

	if sampleRate != deviceSampleRate {
		pcm = voice.ResamplePCM(pcm, sampleRate, deviceSampleRate)
		pcm = voice.NormalizePCM(pcm, 20000)
	}

	clients := h.AllClients()
	var sent int
	var lastErr error
	for _, c := range clients {
		if c.Kind() != kind {
			continue
		}
		if err := c.SendTTS(pcm); err != nil {
			h.logger.Warn("TTS送信失敗", "client", c.ID(), "kind", c.Kind(), "error", err)
			lastErr = err
		} else {
			sent++
		}
	}
	// 非ストリーミング経路でも終端マーカーを送って widget 側の playing state を解除する。
	for _, c := range clients {
		if c.Kind() != kind {
			continue
		}
		if err := c.SendTTSEnd(); err != nil {
			h.logger.Warn("TTS終端送信失敗", "client", c.ID(), "error", err)
		}
	}
	if sent == 0 && lastErr == nil {
		return fmt.Errorf("device: %sクライアント未接続", kind)
	}
	return lastErr
}

// SpeakStreamTo は sentence チャネルから 1 文ずつ TTS 合成 → 対応 kind の
// クライアントに PCM を逐次送信する。終了時に FrameTTSEnd を送ってストリーム終端を通知する。
// TTS 合成と送信はチャネルを介して並行し、first-audio latency を短縮する。
func (h *Hub) SpeakStreamTo(ctx context.Context, sentences <-chan string, kind string) error {
	if h.tts == nil {
		h.logger.Warn("声の合成が設定されていないのでスキップ")
		for range sentences {
		}
		return nil
	}

	// 1 文先行合成: send 中に次の文を用意する。
	pcmQueue := make(chan []byte, 1)
	synthCtx, cancelSynth := context.WithCancel(ctx)
	defer cancelSynth()

	go func() {
		defer close(pcmQueue)
		for sentence := range sentences {
			if synthCtx.Err() != nil {
				for range sentences {
				}
				return
			}
			// Transcript を先に配信 (TTS 合成より前にテキストを見せる)。
			if kind == "web" {
				_ = h.BroadcastCommandTo("web", map[string]any{
					"cmd":  "transcript",
					"kind": "bot",
					"text": sentence,
				})
			}
			pcm, sampleRate, err := h.tts.Synthesize(synthCtx, sentence)
			if err != nil {
				h.logger.Warn("ストリーミング TTS 失敗、スキップ", "error", err, "sentence", sentence)
				continue
			}
			if len(pcm) == 0 {
				continue
			}
			if sampleRate != deviceSampleRate {
				pcm = voice.ResamplePCM(pcm, sampleRate, deviceSampleRate)
				pcm = voice.NormalizePCM(pcm, 20000)
			}
			select {
			case pcmQueue <- pcm:
			case <-synthCtx.Done():
				for range sentences {
				}
				return
			}
		}
	}()

	var sent int
	var lastErr error
	for pcm := range pcmQueue {
		clients := h.AllClients()
		anySent := false
		for _, c := range clients {
			if c.Kind() != kind {
				continue
			}
			if err := c.SendTTS(pcm); err != nil {
				h.logger.Warn("TTS送信失敗", "client", c.ID(), "kind", c.Kind(), "error", err)
				lastErr = err
			} else {
				anySent = true
			}
		}
		if anySent {
			sent++
		}
	}

	// 終端マーカー: 最後のチャンクと共に送ると「この発話はもう終わり」とクライアントが確信できる。
	for _, c := range h.AllClients() {
		if c.Kind() != kind {
			continue
		}
		if err := c.SendTTSEnd(); err != nil {
			h.logger.Warn("TTS終端送信失敗", "client", c.ID(), "error", err)
		}
	}

	if sent == 0 && lastErr == nil {
		return fmt.Errorf("device: %sクライアント未接続", kind)
	}
	return lastErr
}

// StartCaptureLoop starts a goroutine that sends periodic capture commands
// to the connected ESP device for continuous camera streaming.
func (h *Hub) StartCaptureLoop(ctx context.Context, intervalMs int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dev := h.Device()
				if dev == nil {
					continue
				}
				_ = dev.SendCommand(map[string]any{"cmd": "capture"})
			}
		}
	}()
}
