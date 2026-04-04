package device

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/haryoiro/suzuha/internal/event"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024, // 64KB — enough for VGA JPEG
	WriteBufferSize: 8 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Handler returns an http.HandlerFunc that upgrades to WebSocket
// and processes ESP32 device binary frames.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.logger.Error("デバイスの接続に失敗", "error", err)
			return
		}

		deviceID := uuid.NewString()[:8]
		dev := &DeviceConn{conn: conn, id: deviceID}
		conn.SetReadLimit(1 * 1024 * 1024) // 1MB max frame

		h.addClient(dev)
		h.logger.Info("デバイスがつながった", "device_id", deviceID, "remote", r.RemoteAddr)

		defer func() {
			h.removeClient(deviceID)
			conn.Close()
			h.logger.Info("デバイスが離れた", "device_id", deviceID)
		}()

		h.readLoop(dev)
	}
}

// readLoop reads binary/text frames from the device and dispatches them.
func (h *Hub) readLoop(dev *DeviceConn) {
	for {
		msgType, data, err := dev.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				h.logger.Warn("デバイスからの受信でエラー", "error", err)
			}
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			if len(data) < 2 {
				continue
			}
			frameType := data[0]
			payload := data[1:]
			h.handleFrame(dev, frameType, payload)

		case websocket.TextMessage:
			// Text frames are treated as status JSON.
			h.logger.Info("デバイスからテキストが届いた", "data", string(data))
		}
	}
}

// handleFrame dispatches a binary frame by its type.
func (h *Hub) handleFrame(dev *DeviceConn, frameType byte, payload []byte) {
	switch frameType {
	case FrameImage:
		h.handleImage(payload)
	case FrameAudio:
		h.handleAudio(payload, "device")
	case FrameStatus:
		h.handleStatus(payload)
	default:
		h.logger.Warn("デバイスから知らない形式のデータが来た", "type", frameType, "bytes", len(payload))
	}
}

// handleImage delegates JPEG processing to the configured ImageHandler.
func (h *Hub) handleImage(jpeg []byte) {
	if len(jpeg) == 0 || h.images == nil {
		return
	}
	h.images.HandleImage(jpeg)
}

// handleAudio feeds PCM to the ESP VAD and triggers STT on speech end.
func (h *Hub) handleAudio(pcm []byte, source string) {
	if h.stt == nil {
		return
	}

	result := h.espVAD.Process(pcm, time.Now())
	if !result.SpeechEnded {
		return
	}

	h.logger.Info("声が聞こえた、文字起こし開始", "bytes", len(result.Audio), "source", source)
	go h.transcribeAndRespond(result.Audio, source)
}

// transcribeAndRespond runs STT, publishes the text as a user message event,
// then speaks the LLM response via TTS.
func (h *Hub) transcribeAndRespond(pcm []byte, source string) {
	ctx := context.Background()

	text, err := h.stt.Transcribe(ctx, pcm, deviceSampleRate)
	if err != nil {
		h.logger.Error("文字起こしに失敗", "error", err)
		return
	}
	if text == "" {
		return
	}

	h.logger.Info("聞き取れた", "text", text, "source", source)

	// Reset VAD to discard audio accumulated during STT processing
	// (prevents echo of the response).
	h.espVAD.Reset()

	// Publish as message event — agent pipeline will process and respond
	h.bus.Publish(event.NewMessageEvent(source, event.MessagePayload{
		Content:  text,
		UserID:   h.ownerID,
		UserName: h.ownerName,
		IsVoice:  true,
	}))
}

// handleStatus logs the device status JSON.
func (h *Hub) handleStatus(payload []byte) {
	var status map[string]any
	if err := json.Unmarshal(payload, &status); err != nil {
		h.logger.Warn("デバイスのステータスが読めなかった", "error", err, "raw", string(payload))
		return
	}
	h.logger.Info("デバイスの状態を受け取った", "status", status)
}
