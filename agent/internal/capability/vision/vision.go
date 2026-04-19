package vision

import (
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/port/tool"
	"github.com/haryoiro/suzuha/internal/runtime/event"
)

// Service は物体検出・追跡・変化通知を提供する vision capability。
// pipeline (hot path) + tools (LLM 公開) + SSE 配信用の Frames を集約する。
type Service struct {
	frames   *FrameStore
	changes  *ChangeDetector
	tracker  *ObjectTracker
	pipeline *Pipeline
	dev      deviceCommander
	vision   VisionDescriber
	logger   *slog.Logger
}

// New は vision Service を作成する。
// dev はデバイスコマンド送信用、vision は VLM 画像説明用 (nil 可)。
func New(bus *event.Bus, yoloURL, defaultChannel string, servo servoCommander, dev deviceCommander, vision VisionDescriber, logger *slog.Logger) *Service {
	var yolo *YOLOClient
	if yoloURL != "" {
		yolo = NewYOLOClient(yoloURL)
	}
	frames := NewFrameStore()
	changes := NewChangeDetector(bus, 30*time.Second, defaultChannel)
	tracker := NewObjectTracker(DefaultTrackerConfig(), servo, logger)
	pipeline := NewPipeline(frames, changes, tracker, yolo, logger)

	return &Service{
		frames:   frames,
		changes:  changes,
		tracker:  tracker,
		pipeline: pipeline,
		dev:      dev,
		vision:   vision,
		logger:   logger,
	}
}

// Tools は LLM に公開するツール群を返す。dev が nil なら空。
func (s *Service) Tools() []tool.Tool {
	if s.dev == nil {
		return nil
	}
	return []tool.Tool{
		newServoTool(s.dev, s.tracker),
		newCaptureTool(s.dev),
		newFaceTool(s.dev),
		newLookTool(s.dev, s.frames, s.vision),
	}
}

// Pipeline returns the image processing pipeline (implements device.ImageHandler).
func (s *Service) Pipeline() *Pipeline { return s.pipeline }

// Frames returns the FrameStore for registering HTTP handlers.
func (s *Service) Frames() *FrameStore { return s.frames }

// ChangeDetector returns the change detector for API access.
func (s *Service) ChangeDetector() *ChangeDetector { return s.changes }

// Tracker returns the object tracker for API access.
func (s *Service) Tracker() *ObjectTracker { return s.tracker }
