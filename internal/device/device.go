package device

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/voice"
)

// Frame type constants matching firmware/main/config.h.
const (
	FrameAudio   = 0x01 // PCM16 16kHz mono  (ESP32 → Server)
	FrameImage   = 0x02 // JPEG              (ESP32 → Server)
	FrameCommand = 0x03 // JSON              (Server → ESP32)
	FrameStatus  = 0x04 // JSON              (ESP32 → Server)
	FrameTTS     = 0x05 // PCM 24kHz mono    (Server → ESP32)
)

// TTS chunk size: 4KB per frame to avoid overwhelming ESP32 ring buffer.
const ttsChunkSize = 4096

// Speaker is the interface that the agent uses to send TTS audio to the device.
type Speaker interface {
	SpeakText(ctx context.Context, text string) error
}

// DeviceConn represents a connected physical device.
type DeviceConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
	id   string
}

// SendCommand sends a JSON command frame to the device.
func (d *DeviceConn) SendCommand(cmd map[string]any) error {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("device: JSONエンコード失敗: %w", err)
	}
	frame := make([]byte, 1+len(payload))
	frame[0] = FrameCommand
	copy(frame[1:], payload)

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// SendTTS sends PCM audio data as TTS frames, chunked to avoid buffer overflow.
func (d *DeviceConn) SendTTS(pcm []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for offset := 0; offset < len(pcm); offset += ttsChunkSize {
		end := offset + ttsChunkSize
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := pcm[offset:end]

		frame := make([]byte, 1+len(chunk))
		frame[0] = FrameTTS
		copy(frame[1:], chunk)

		if err := d.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			return fmt.Errorf("device: TTS送信失敗: %w", err)
		}
	}
	return nil
}

// Hub manages device connections.
type Hub struct {
	mu      sync.RWMutex
	device  *DeviceConn
	bus     *event.Bus
	tts     voice.TTS
	yolo    *YOLOClient
	frames  *FrameStore
	changes *ChangeDetector
	logger  *slog.Logger
}

// NewHub creates a new device Hub.
// defaultChannel is the Discord channel ID for vision change notifications.
func NewHub(bus *event.Bus, tts voice.TTS, yoloURL, defaultChannel string, logger *slog.Logger) *Hub {
	var yolo *YOLOClient
	if yoloURL != "" {
		yolo = NewYOLOClient(yoloURL)
	}
	return &Hub{
		bus:     bus,
		tts:     tts,
		yolo:    yolo,
		frames:  NewFrameStore(),
		changes: NewChangeDetector(bus, 30*time.Second, defaultChannel),
		logger:  logger,
	}
}

// ChangeDetector returns the change detector for API access.
func (h *Hub) ChangeDetector() *ChangeDetector {
	return h.changes
}

// Frames returns the FrameStore for registering HTTP handlers.
func (h *Hub) Frames() *FrameStore {
	return h.frames
}

// Device returns the currently connected device, or nil.
func (h *Hub) Device() *DeviceConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.device
}

// IsConnected returns true if a device is connected.
func (h *Hub) IsConnected() bool {
	return h.Device() != nil
}

// SpeakText synthesizes text to PCM via TTS and sends it to the connected device.
func (h *Hub) SpeakText(ctx context.Context, text string) error {
	dev := h.Device()
	if dev == nil {
		return fmt.Errorf("device: デバイス未接続")
	}
	if h.tts == nil {
		h.logger.Warn("device: TTSクライアント未設定、テキスト応答をスキップ")
		return nil
	}

	pcm, err := h.tts.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("device: TTS合成失敗: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}

	h.logger.Info("device: TTS送信", "pcm_bytes", len(pcm))
	return dev.SendTTS(pcm)
}

// StartCaptureLoop starts a goroutine that sends periodic capture commands
// to the connected device for continuous camera streaming.
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

func (h *Hub) setDevice(d *DeviceConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.device = d
}

func (h *Hub) clearDevice(d *DeviceConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.device == d {
		h.device = nil
	}
}
