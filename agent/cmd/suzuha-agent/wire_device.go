package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/haryoiro/suzuha/external/stt"
	"github.com/haryoiro/suzuha/external/tts"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/adapter/device"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/feature/vision"
	"github.com/haryoiro/suzuha/internal/gateway"
	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/samber/do/v2"
)

// registerDeviceHandlers は物理デバイス (ESP32) 関連のハンドラを登録する。
// 返される CancelFunc はキャプチャループの停止に使用する。
func registerDeviceHandlers(mux *http.ServeMux, injector do.Injector, ag *agent.Agent, gw *gateway.Gateway) context.CancelFunc {
	cfg := do.MustInvoke[*config.Config](injector)
	logger := do.MustInvoke[*slog.Logger](injector)
	bus := do.MustInvoke[*event.Bus](injector)

	var ttsClient tts.TTS
	if cfg.Voice.Enabled && len(cfg.Voice.TTS) > 0 {
		deviceTTSConfigs := make([]tts.TTSProviderConfig, len(cfg.Voice.TTS))
		for i, p := range cfg.Voice.TTS {
			deviceTTSConfigs[i] = tts.TTSProviderConfig{
				Provider:  p.Provider,
				URL:       p.URL,
				SpeakerID: p.SpeakerID,
				Model:     p.Model,
				Style:     p.Style,
			}
		}
		var err error
		ttsClient, err = tts.NewTTSChain(deviceTTSConfigs, logger)
		if err != nil {
			logger.Error("TTS クライアントの初期化に失敗", "error", err)
		}
	}
	yoloURL := os.Getenv("YOLO_URL")
	if yoloURL == "" {
		yoloURL = "http://yolo:8002"
	}
	// Look up home channel from DB.
	var deviceChannel string
	db := do.MustInvokeNamed[*sql.DB](injector, "shared-db")
	if err := db.QueryRow("SELECT channel_id FROM channel_settings WHERE home = true LIMIT 1").Scan(&deviceChannel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Error("ホームチャンネルの取得に失敗", "error", err)
	}
	var sttClient stt.STT
	if cfg.Voice.Enabled && len(cfg.Voice.STT) > 0 {
		var err error
		sttClient, err = stt.NewSTT(stt.STTProviderConfig{
			Provider: cfg.Voice.STT[0].Provider,
			APIKey:   cfg.Voice.STT[0].APIKey,
			Model:    cfg.Voice.STT[0].Model,
			URL:      cfg.Voice.STT[0].URL,
		})
		if err != nil {
			logger.Error("STT クライアントの初期化に失敗", "error", err)
		}
	}
	// Look up owner from DB
	var ownerID, ownerName string
	if err := db.QueryRow("SELECT id, display_name FROM users WHERE role = 'owner' LIMIT 1").Scan(&ownerID, &ownerName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Error("オーナー情報の取得に失敗", "error", err)
	}
	if ownerID == "" {
		ownerID = "owner"
		ownerName = "オーナー"
	}
	hub := device.NewHub(bus, ttsClient, sttClient, ownerID, ownerName, logger)
	devAdapter := device.NewDeviceAdapter(hub)
	visionFeature := vision.New(bus, yoloURL, deviceChannel, devAdapter, devAdapter,
		do.MustInvoke[*llm.Client](injector), logger)
	hub.SetImageHandler(visionFeature.Pipeline())
	do.ProvideValue(injector, visionFeature)
	mux.HandleFunc("GET /ws/device", hub.Handler())
	mux.HandleFunc("GET /ws/web", hub.WebHandler())
	mux.HandleFunc("GET /internal/device/frame", visionFeature.Frames().FrameHandler())
	mux.HandleFunc("GET /internal/device/detections", visionFeature.Frames().DetectionStreamHandler())
	ag.SetSession(agent.SourceKeyDevice, agent.NewDeviceSession(
		ag.AgentContextFor(agent.SourceKeyDevice), hub, logger,
	))
	gw.Register(device.NewSource(hub))

	mux.HandleFunc("GET /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"enabled": visionFeature.ChangeDetector().Enabled()})
	})
	mux.HandleFunc("PUT /internal/device/vision", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		visionFeature.ChangeDetector().SetEnabled(body.Enabled)
		logger.Info("device: 視界変化検出の切り替え", "enabled", body.Enabled)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("POST /internal/device/servo", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Pan  int `json:"pan"`
			Tilt int `json:"tilt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		dev := hub.Device()
		if dev == nil {
			http.Error(w, `{"error":"device not connected"}`, http.StatusServiceUnavailable)
			return
		}
		if err := dev.SendCommand(map[string]any{"cmd": "servo", "pan": body.Pan, "tilt": body.Tilt}); err != nil {
			http.Error(w, `{"error":"send failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"pan":%d,"tilt":%d}`, body.Pan, body.Tilt)
	})

	mux.HandleFunc("PUT /internal/device/volume", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Level int `json:"level"` // 0-100
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		dev := hub.Device()
		if dev == nil {
			http.Error(w, `{"error":"device not connected"}`, http.StatusServiceUnavailable)
			return
		}
		if err := dev.SendCommand(map[string]any{"cmd": "volume", "level": body.Level}); err != nil {
			http.Error(w, `{"error":"send failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"level":%d}`, body.Level)
	})

	// Object tracker API
	mux.HandleFunc("GET /internal/device/tracker", func(w http.ResponseWriter, r *http.Request) {
		tr := visionFeature.Tracker()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"enabled": tr.Enabled(),
			"config":  tr.Config(),
		})
	})
	mux.HandleFunc("PUT /internal/device/tracker", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled          *bool    `json:"enabled"`
			TargetLabel      *string  `json:"target_label"`
			DeadZone         *float64 `json:"dead_zone"`
			SmoothingAlpha   *float64 `json:"smoothing_alpha"`
			ProportionalGain *float64 `json:"proportional_gain"`
			MaxDegPerFrame   *float64 `json:"max_deg_per_frame"`
			InvertPan        *bool    `json:"invert_pan"`
			InvertTilt       *bool    `json:"invert_tilt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		tr := visionFeature.Tracker()
		if body.Enabled != nil {
			tr.SetEnabled(*body.Enabled)
			logger.Info("device: トラッカー切り替え", "enabled", *body.Enabled)
		}
		if body.TargetLabel != nil {
			tr.SetTargetLabel(*body.TargetLabel)
		}
		tr.ApplyPartial(vision.TrackerPatch{
			DeadZone:         body.DeadZone,
			SmoothingAlpha:   body.SmoothingAlpha,
			ProportionalGain: body.ProportionalGain,
			MaxDegPerFrame:   body.MaxDegPerFrame,
			InvertPan:        body.InvertPan,
			InvertTilt:       body.InvertTilt,
		})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})

	// Start periodic capture loop (333ms = ~3fps).
	captureCtx, captureCancel := context.WithCancel(context.Background())
	hub.StartCaptureLoop(captureCtx, 333)
	logger.Info("デバイス接続口を開いた")

	return captureCancel
}
