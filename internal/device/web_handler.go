package device

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/haryoiro/suzuha/internal/voice"
)

// webAudioState holds per-connection VAD for web clients.
type webAudioState struct {
	mu  sync.Mutex
	vad *voice.VAD
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
	vad := voice.NewVAD()
	vad.SpeechThreshold = 100
	vad.SilenceDuration = time.Second
	vad.MinSpeechDuration = 2 * time.Second
	vad.MaxSpeechDuration = 10 * time.Second
	state := &webAudioState{vad: vad}

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

// handleWebAudio processes audio from a web client with per-connection VAD.
func (h *Hub) handleWebAudio(state *webAudioState, pcm []byte) {
	if h.stt == nil {
		return
	}

	state.mu.Lock()
	result := state.vad.Process(pcm, time.Now())
	state.mu.Unlock()

	if !result.SpeechEnded {
		return
	}

	h.logger.Info("Webクライアントの声が聞こえた、文字起こし開始", "bytes", len(result.Audio))
	go h.transcribeAndRespond(result.Audio, "web")
}
