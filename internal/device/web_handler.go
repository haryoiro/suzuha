package device

import (
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// webAudioState holds per-connection audio buffer for web clients.
type webAudioState struct {
	mu           sync.Mutex
	audioBuf     []byte
	silenceCount int // consecutive silent chunks
}

// WebHandler returns an http.HandlerFunc that upgrades to WebSocket
// and processes browser client binary frames.
func (h *Hub) WebHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.logger.Error("Webクライアントの接続に失敗", "error", err)
			return
		}

		clientID := "web-" + uuid.NewString()[:8]
		wc := &WebConn{conn: conn, id: clientID}
		conn.SetReadLimit(256 * 1024) // 256KB max frame

		h.addClient(wc)
		h.logger.Info("Webクライアントがつながった", "client_id", clientID, "remote", r.RemoteAddr)

		defer func() {
			h.removeClient(clientID)
			conn.Close()
			h.logger.Info("Webクライアントが離れた", "client_id", clientID)
		}()

		h.webReadLoop(wc)
	}
}

// webReadLoop reads binary frames from a web client.
func (h *Hub) webReadLoop(wc *WebConn) {
	state := &webAudioState{}

	for {
		msgType, data, err := wc.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Warn("Webクライアントからの受信でエラー", "client_id", wc.id, "error", err)
			}
			return
		}

		if msgType != websocket.BinaryMessage || len(data) < 2 {
			continue
		}

		frameType := data[0]
		payload := data[1:]

		switch frameType {
		case FrameAudio:
			h.handleWebAudio(state, payload)
		default:
			h.logger.Debug("Webクライアントから未対応フレーム", "type", frameType, "client_id", wc.id)
		}
	}
}

// handleWebAudio processes audio from a web client with per-connection buffer.
// Requires consecutive silent chunks before triggering transcription to avoid
// cutting speech at brief pauses.
func (h *Hub) handleWebAudio(state *webAudioState, pcm []byte) {
	if h.stt == nil {
		return
	}

	state.mu.Lock()
	state.audioBuf = append(state.audioBuf, pcm...)

	const minBytes = deviceSampleRate * 2 * 2  // 2s minimum
	const maxBytes = deviceSampleRate * 2 * 10 // 10s maximum
	const silenceThreshold = 100
	const requiredSilenceChunks = 10 // ~1s of consecutive silence (100ms chunks)

	if len(state.audioBuf) < minBytes {
		state.mu.Unlock()
		return
	}

	// Energy check on the latest chunk
	energy := int64(0)
	samples := len(pcm) / 2
	for i := 0; i < samples; i++ {
		s := int16(uint16(pcm[i*2]) | uint16(pcm[i*2+1])<<8)
		if s < 0 {
			s = -s
		}
		energy += int64(s)
	}
	avgEnergy := energy / int64(samples+1)

	if avgEnergy < silenceThreshold {
		state.silenceCount++
	} else {
		state.silenceCount = 0
	}

	// Trigger transcription only after sustained silence or max buffer
	if state.silenceCount >= requiredSilenceChunks || len(state.audioBuf) >= maxBytes {
		// Skip if only silence (no speech detected at all)
		totalEnergy := int64(0)
		totalSamples := len(state.audioBuf) / 2
		for i := 0; i < totalSamples; i++ {
			s := int16(uint16(state.audioBuf[i*2]) | uint16(state.audioBuf[i*2+1])<<8)
			if s < 0 {
				s = -s
			}
			totalEnergy += int64(s)
		}
		if totalEnergy/int64(totalSamples+1) < silenceThreshold {
			state.audioBuf = nil
			state.silenceCount = 0
			state.mu.Unlock()
			return
		}

		buf := state.audioBuf
		state.audioBuf = nil
		state.silenceCount = 0
		state.mu.Unlock()

		h.logger.Info("Webクライアントの声が聞こえた、文字起こし開始", "bytes", len(buf))
		go h.transcribeAndRespond(buf, "web")
	} else {
		state.mu.Unlock()
	}
}
