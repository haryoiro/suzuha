package device

import (
	"context"
	"encoding/json"
	"net/http"

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

// handleImage stores the frame and runs YOLO detection asynchronously.
// Does NOT publish to agent event bus (V1: admin-only view).
func (h *Hub) handleImage(jpeg []byte) {
	if len(jpeg) == 0 {
		return
	}

	// Store latest frame for admin viewing.
	h.frames.UpdateFrame(jpeg)

	// Run YOLO detection asynchronously.
	if h.yolo != nil {
		go func() {
			result, err := h.yolo.Detect(context.Background(), jpeg)
			if err != nil {
				h.logger.Debug("物体検出に失敗", "error", err)
				return
			}
			// ESP32-CAM sends 640x480.
			h.frames.UpdateDetections(result, 640, 480)

			// Notify agent on significant changes.
			if h.changes.Update(result.Detections) {
				h.logger.Info("視界に変化があった")
			}
		}()
	}
}

// handleAudio accumulates PCM chunks and runs STT when enough audio is buffered.
// Requires consecutive silent chunks before triggering to avoid cutting speech at pauses.
// source is the event source string ("device" or "web").
func (h *Hub) handleAudio(pcm []byte, source string) {
	if h.stt == nil {
		return
	}

	h.audioBuf = append(h.audioBuf, pcm...)

	// Accumulate at least 2s of audio (24kHz, 16-bit mono)
	const minBytes = deviceSampleRate * 2 * 2  // 2s minimum
	const maxBytes = deviceSampleRate * 2 * 10 // 10s maximum
	const silenceThreshold = 100
	const requiredSilenceChunks = 10 // ~1s of consecutive silence (100ms chunks)

	if len(h.audioBuf) < minBytes {
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
		h.silenceCount++
	} else {
		h.silenceCount = 0
	}

	// Trigger transcription only after sustained silence or max buffer
	if h.silenceCount >= requiredSilenceChunks || len(h.audioBuf) >= maxBytes {
		// Skip if only silence (no speech detected at all)
		totalEnergy := int64(0)
		totalSamples := len(h.audioBuf) / 2
		for i := 0; i < totalSamples; i++ {
			s := int16(uint16(h.audioBuf[i*2]) | uint16(h.audioBuf[i*2+1])<<8)
			if s < 0 {
				s = -s
			}
			totalEnergy += int64(s)
		}
		if totalEnergy/int64(totalSamples+1) < silenceThreshold {
			h.audioBuf = nil
			h.silenceCount = 0
			return
		}

		buf := h.audioBuf
		h.audioBuf = nil
		h.silenceCount = 0

		h.logger.Info("声が聞こえた、文字起こし開始", "bytes", len(buf), "source", source)
		go h.transcribeAndRespond(buf, source)
	}
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

	// Clear any audio buffer accumulated during STT processing
	// (prevents echo of the response)
	h.audioBuf = nil

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
