package device

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

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
