package vision

import (
	"context"
	"log/slog"

	"github.com/haryoiro/suzuha/external/detect"
	"golang.org/x/sync/semaphore"
)

// Pipeline processes JPEG frames: stores them, runs YOLO detection,
// updates change detector and object tracker.
// It implements device.ImageHandler.
type Pipeline struct {
	frames    *FrameStore
	changes   *ChangeDetector
	tracker   *ObjectTracker
	yolo      *detect.YOLOClient
	detectSem *semaphore.Weighted
	logger    *slog.Logger
}

// NewPipeline creates a vision processing pipeline.
func NewPipeline(frames *FrameStore, changes *ChangeDetector, tracker *ObjectTracker, yolo *detect.YOLOClient, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		frames:    frames,
		changes:   changes,
		tracker:   tracker,
		yolo:      yolo,
		detectSem: semaphore.NewWeighted(1),
		logger:    logger,
	}
}

// HandleImage stores the frame and runs YOLO detection asynchronously.
func (p *Pipeline) HandleImage(jpeg []byte) {
	p.frames.UpdateFrame(jpeg)

	if p.yolo != nil && p.detectSem.TryAcquire(1) {
		go func() {
			defer p.detectSem.Release(1)

			result, err := p.yolo.Detect(context.Background(), jpeg)
			if err != nil {
				p.logger.Debug("物体検出に失敗", "error", err)
				return
			}
			// ESP32-CAM sends 640x480.
			p.frames.UpdateDetections(result, 640, 480)

			if p.changes.Update(result.Detections) {
				p.logger.Info("視界に変化があった")
			}

			if p.tracker != nil {
				p.tracker.Feed(result.Detections)
			}
		}()
	}
}
