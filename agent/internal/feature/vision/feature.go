package vision

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/external/detect"
	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/tool"
)

// Feature は物体検出・追跡・変化通知を提供する。
type Feature struct {
	frames   *FrameStore
	changes  *ChangeDetector
	tracker  *ObjectTracker
	pipeline *Pipeline
	dev      deviceCommander
	vision   VisionDescriber
	logger   *slog.Logger
}

// New は vision Feature を作成する。
// dev はデバイスコマンド送信用、vision は VLM 画像説明用 (nil 可)。
func New(bus *event.Bus, yoloURL, defaultChannel string, servo servoCommander, dev deviceCommander, vision VisionDescriber, logger *slog.Logger) *Feature {
	var yolo *detect.YOLOClient
	if yoloURL != "" {
		yolo = detect.NewYOLOClient(yoloURL)
	}
	frames := NewFrameStore()
	changes := NewChangeDetector(bus, 30*time.Second, defaultChannel)
	tracker := NewObjectTracker(DefaultTrackerConfig(), servo, logger)
	pipeline := NewPipeline(frames, changes, tracker, yolo, logger)

	return &Feature{
		frames:   frames,
		changes:  changes,
		tracker:  tracker,
		pipeline: pipeline,
		dev:      dev,
		vision:   vision,
		logger:   logger,
	}
}

func (f *Feature) Name() string                             { return "vision" }
func (f *Feature) Setup(_ context.Context, _ *sql.DB) error { return nil }
func (f *Feature) Tasks() []scheduler.CronTask              { return nil }

func (f *Feature) Tools() []tool.Tool {
	if f.dev == nil {
		return nil
	}
	return []tool.Tool{
		newServoTool(f.dev, f.tracker),
		newCaptureTool(f.dev),
		newFaceTool(f.dev),
		newLookTool(f.dev, f.frames, f.vision),
	}
}

// Pipeline returns the image processing pipeline (implements device.ImageHandler).
func (f *Feature) Pipeline() *Pipeline { return f.pipeline }

// Frames returns the FrameStore for registering HTTP handlers.
func (f *Feature) Frames() *FrameStore { return f.frames }

// ChangeDetector returns the change detector for API access.
func (f *Feature) ChangeDetector() *ChangeDetector { return f.changes }

// Tracker returns the object tracker for API access.
func (f *Feature) Tracker() *ObjectTracker { return f.tracker }

var _ scheduler.Feature = (*Feature)(nil)
