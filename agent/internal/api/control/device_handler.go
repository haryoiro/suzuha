package control

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/capability/vision"
	"github.com/haryoiro/suzuha/internal/channel/device"
	"github.com/samber/do/v2"
)

// DeviceHandler は Device グループ (vision / servo / volume / tracker) を実装する。
// 物理デバイス (ESP32) への JSON コマンド送信と vision 設定を束ねる。
type DeviceHandler struct {
	vision *vision.Service
	hub    *device.Hub
}

// NewDeviceHandler は DI injector から依存を取り出して DeviceHandler を生成する。
func NewDeviceHandler(i do.Injector) (gen.DeviceHandler, error) {
	return &DeviceHandler{
		vision: do.MustInvoke[*vision.Service](i),
		hub:    do.MustInvoke[*device.Hub](i),
	}, nil
}

// DeviceGetVision implements GET /internal/device/vision.
func (h *DeviceHandler) DeviceGetVision(ctx context.Context) (*gen.VisionStatus, error) {
	return &gen.VisionStatus{Enabled: h.vision.ChangeDetector().Enabled()}, nil
}

// DeviceSetVision implements PUT /internal/device/vision.
func (h *DeviceHandler) DeviceSetVision(ctx context.Context, req *gen.SetVisionRequest) (*gen.OkResponse, error) {
	h.vision.ChangeDetector().SetEnabled(req.Enabled)
	return &gen.OkResponse{Ok: true}, nil
}

// DeviceServo implements POST /internal/device/servo.
func (h *DeviceHandler) DeviceServo(ctx context.Context, req *gen.ServoRequest) (*gen.ServoResponse, error) {
	dev := h.hub.Device()
	if dev == nil {
		return nil, fmt.Errorf("device not connected")
	}
	pan, tilt := int(req.Pan), int(req.Tilt)
	if err := dev.SendCommand(map[string]any{"cmd": "servo", "pan": pan, "tilt": tilt}); err != nil {
		return nil, fmt.Errorf("servo 送信失敗: %w", err)
	}
	return &gen.ServoResponse{Ok: true, Pan: int32(pan), Tilt: int32(tilt)}, nil
}

// DeviceVolume implements PUT /internal/device/volume.
func (h *DeviceHandler) DeviceVolume(ctx context.Context, req *gen.VolumeRequest) (*gen.VolumeResponse, error) {
	dev := h.hub.Device()
	if dev == nil {
		return nil, fmt.Errorf("device not connected")
	}
	level := int(req.Level)
	if err := dev.SendCommand(map[string]any{"cmd": "volume", "level": level}); err != nil {
		return nil, fmt.Errorf("volume 送信失敗: %w", err)
	}
	return &gen.VolumeResponse{Ok: true, Level: int32(level)}, nil
}

// DeviceGetTracker implements GET /internal/device/tracker.
func (h *DeviceHandler) DeviceGetTracker(ctx context.Context) (*gen.TrackerStatus, error) {
	tr := h.vision.Tracker()
	return &gen.TrackerStatus{
		Enabled: tr.Enabled(),
		Config:  structToJxMap[gen.TrackerStatusConfig](tr.Config()),
	}, nil
}

// DevicePatchTracker implements PUT /internal/device/tracker.
func (h *DeviceHandler) DevicePatchTracker(ctx context.Context, req *gen.TrackerPatch) (*gen.OkResponse, error) {
	tr := h.vision.Tracker()
	if v, ok := req.Enabled.Get(); ok {
		tr.SetEnabled(v)
	}
	if v, ok := req.TargetLabel.Get(); ok {
		tr.SetTargetLabel(v)
	}
	patch := vision.TrackerPatch{}
	if v, ok := req.DeadZone.Get(); ok {
		patch.DeadZone = &v
	}
	if v, ok := req.SmoothingAlpha.Get(); ok {
		patch.SmoothingAlpha = &v
	}
	if v, ok := req.ProportionalGain.Get(); ok {
		patch.ProportionalGain = &v
	}
	if v, ok := req.MaxDegPerFrame.Get(); ok {
		patch.MaxDegPerFrame = &v
	}
	if v, ok := req.InvertPan.Get(); ok {
		patch.InvertPan = &v
	}
	if v, ok := req.InvertTilt.Get(); ok {
		patch.InvertTilt = &v
	}
	tr.ApplyPartial(patch)
	return &gen.OkResponse{Ok: true}, nil
}
