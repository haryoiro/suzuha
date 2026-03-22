package device

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// Client is a connected display+speaker endpoint (ESP32 or browser).
type Client interface {
	ID() string
	Kind() string // "esp" or "web"
	SendCommand(cmd map[string]any) error
	SendTTS(pcm []byte) error
	Close() error
}

// --- DeviceConn implements Client for ESP32 devices ---

// DeviceConn represents a connected physical device (ESP32).
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

func (d *DeviceConn) ID() string   { return d.id }
func (d *DeviceConn) Kind() string { return "esp" }
func (d *DeviceConn) Close() error { return d.conn.Close() }

// --- WebConn implements Client for browser WebSocket connections ---

// WebConn represents a connected browser client.
type WebConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
	id   string
}

func (w *WebConn) ID() string   { return w.id }
func (w *WebConn) Kind() string { return "web" }

func (w *WebConn) SendCommand(cmd map[string]any) error {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("web: JSONエンコード失敗: %w", err)
	}
	frame := make([]byte, 1+len(payload))
	frame[0] = FrameCommand
	copy(frame[1:], payload)

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (w *WebConn) SendTTS(pcm []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Web clients don't have the ESP32's 4KB buffer limitation,
	// but we chunk to avoid huge single frames.
	const chunkSize = 16000 // ~333ms at 24kHz 16-bit mono
	for offset := 0; offset < len(pcm); offset += chunkSize {
		end := offset + chunkSize
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := pcm[offset:end]

		frame := make([]byte, 1+len(chunk))
		frame[0] = FrameTTS
		copy(frame[1:], chunk)

		if err := w.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			return fmt.Errorf("web: TTS送信失敗: %w", err)
		}
	}
	return nil
}

func (w *WebConn) Close() error { return w.conn.Close() }
