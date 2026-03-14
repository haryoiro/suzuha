package device

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024, // 64KB — enough for VGA JPEG
	WriteBufferSize: 8 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Handler returns an http.HandlerFunc that upgrades to WebSocket
// and processes device binary frames.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			h.logger.Error("device: WebSocketアップグレード失敗", "error", err)
			return
		}

		deviceID := uuid.NewString()[:8]
		dev := &DeviceConn{conn: conn, id: deviceID}
		conn.SetReadLimit(1 * 1024 * 1024) // 1MB max frame

		h.setDevice(dev)
		h.logger.Info("device: 接続", "device_id", deviceID, "remote", r.RemoteAddr)

		defer func() {
			h.clearDevice(dev)
			conn.Close()
			h.logger.Info("device: 切断", "device_id", deviceID)
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
				h.logger.Warn("device: 読み取りエラー", "error", err)
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
			h.logger.Info("device: テキストフレーム受信", "data", string(data))
		}
	}
}

// handleFrame dispatches a binary frame by its type.
func (h *Hub) handleFrame(dev *DeviceConn, frameType byte, payload []byte) {
	switch frameType {
	case FrameImage:
		h.handleImage(payload)
	case FrameAudio:
		// V1: ESP32-CAM has no mic, skip audio frames.
		h.logger.Debug("device: オーディオフレーム受信（スキップ）", "bytes", len(payload))
	case FrameStatus:
		h.handleStatus(payload)
	default:
		h.logger.Warn("device: 不明なフレームタイプ", "type", frameType, "bytes", len(payload))
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
				h.logger.Debug("device: YOLO検出失敗", "error", err)
				return
			}
			// ESP32-CAM sends 640x480.
			h.frames.UpdateDetections(result, 640, 480)

			// Notify agent on significant changes.
			if h.changes.Update(result.Detections) {
				h.logger.Info("device: 視界変化を検出")
			}
		}()
	}
}

// handleStatus logs the device status JSON.
func (h *Hub) handleStatus(payload []byte) {
	var status map[string]any
	if err := json.Unmarshal(payload, &status); err != nil {
		h.logger.Warn("device: ステータスJSON解析失敗", "error", err, "raw", string(payload))
		return
	}
	h.logger.Info("device: ステータス受信", "status", status)
}
