package vision

import (
	"log/slog"
	"math"
	"sync"

	"github.com/haryoiro/suzuha/external/detect"
)

// servoCommander sends servo commands to a physical device.
// Defined here (consumer-side) to avoid importing device package.
type servoCommander interface {
	SendServo(pan, tilt int) error
}

// TrackerConfig holds tunable parameters for the object tracker.
type TrackerConfig struct {
	TargetLabel      string  `json:"target_label"`      // lock to single label ("" = cascade mode)
	ConfirmFrames    int     `json:"confirm_frames"`    // consecutive detections to confirm
	LostFrames       int     `json:"lost_frames"`       // consecutive misses to drop
	IoUThreshold     float64 `json:"iou_threshold"`     // min IoU for matching
	MinConfidence    float64 `json:"min_confidence"`    // ignore detections below this
	SmoothingAlpha   float64 `json:"smoothing_alpha"`   // EMA alpha (0-1)
	DeadZone         float64 `json:"dead_zone"`         // fraction of frame (0-1)
	ProportionalGain float64 `json:"proportional_gain"` // deg per pixel
	MaxDegPerFrame   float64 `json:"max_deg_per_frame"`
	FrameWidth       int     `json:"frame_width"`
	FrameHeight      int     `json:"frame_height"`
	InvertPan        bool    `json:"invert_pan"`
	InvertTilt       bool    `json:"invert_tilt"`
}

// labelPriority returns the tracking priority for a label.
// Higher = preferred. 0 = not tracked.
func labelPriority(label string) int {
	switch label {
	case "face":
		return 4
	case "head", "head_front":
		return 3
	case "head_back":
		return 2
	default:
		return 0
	}
}

// targetPoint returns the point to aim at for a given detection.
func targetPoint(d detect.Detection) (float64, float64) {
	if d.Label == "body" {
		cx := float64(d.BBox[0]+d.BBox[2]) / 2.0
		top := float64(d.BBox[1])
		h := float64(d.BBox[3] - d.BBox[1])
		return cx, top + h*0.12
	}
	return bboxCenter(d.BBox)
}

// DefaultTrackerConfig returns sane defaults for ~3 FPS tracking.
func DefaultTrackerConfig() TrackerConfig {
	return TrackerConfig{
		TargetLabel:      "",
		ConfirmFrames:    3,
		LostFrames:       5,
		IoUThreshold:     0.3,
		MinConfidence:    0.4,
		SmoothingAlpha:   0.3,
		DeadZone:         0.08,
		ProportionalGain: 0.15,
		MaxDegPerFrame:   5.0,
		FrameWidth:       640,
		FrameHeight:      480,
		InvertPan:        false,
		InvertTilt:       false,
	}
}

type trackState int

const (
	trackTentative trackState = iota
	trackConfirmed
	trackLost
)

type trackedObject struct {
	id         int
	label      string
	bbox       [4]int
	confidence float64
	state      trackState
	seenCount  int
	missCount  int
	smoothX    float64
	smoothY    float64
	area       int
}

// ObjectTracker performs LLM-free object tracking and servo control.
type ObjectTracker struct {
	mu          sync.Mutex
	cfg         TrackerConfig
	enabled     bool
	objects     []*trackedObject
	nextID      int
	currentPan  float64
	currentTilt float64
	logger      *slog.Logger
	servo       servoCommander
}

// NewObjectTracker creates a tracker.
func NewObjectTracker(cfg TrackerConfig, servo servoCommander, logger *slog.Logger) *ObjectTracker {
	return &ObjectTracker{
		cfg:         cfg,
		currentPan:  90,
		currentTilt: 90,
		logger:      logger,
		servo:       servo,
	}
}

func (t *ObjectTracker) SetEnabled(v bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = v
	if !v {
		t.objects = nil
	}
}

func (t *ObjectTracker) Enabled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enabled
}

func (t *ObjectTracker) SetTargetLabel(label string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg.TargetLabel = label
}

// UpdatePosition syncs the tracker's internal servo state with an external move
// (e.g. LLM tool call).
func (t *ObjectTracker) UpdatePosition(pan, tilt float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentPan = pan
	t.currentTilt = tilt
}

// Config returns a copy of the current config.
func (t *ObjectTracker) Config() TrackerConfig {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg
}

// TrackerPatch allows partial updates with pointer fields (nil = no change).
type TrackerPatch struct {
	DeadZone         *float64
	SmoothingAlpha   *float64
	ProportionalGain *float64
	MaxDegPerFrame   *float64
	InvertPan        *bool
	InvertTilt       *bool
}

// ApplyPartial applies non-nil fields from the patch.
func (t *ObjectTracker) ApplyPartial(p TrackerPatch) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p.DeadZone != nil {
		t.cfg.DeadZone = *p.DeadZone
	}
	if p.SmoothingAlpha != nil {
		t.cfg.SmoothingAlpha = *p.SmoothingAlpha
	}
	if p.ProportionalGain != nil {
		t.cfg.ProportionalGain = *p.ProportionalGain
	}
	if p.MaxDegPerFrame != nil {
		t.cfg.MaxDegPerFrame = *p.MaxDegPerFrame
	}
	if p.InvertPan != nil {
		t.cfg.InvertPan = *p.InvertPan
	}
	if p.InvertTilt != nil {
		t.cfg.InvertTilt = *p.InvertTilt
	}
}

// Feed processes a new set of YOLO detections.
func (t *ObjectTracker) Feed(detections []detect.Detection) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.enabled {
		return
	}

	// Step 1: Filter detections
	var relevant []detect.Detection
	for _, d := range detections {
		if d.Confidence < t.cfg.MinConfidence {
			continue
		}
		if t.cfg.TargetLabel != "" {
			if d.Label != t.cfg.TargetLabel {
				continue
			}
		} else {
			if labelPriority(d.Label) == 0 {
				continue
			}
		}
		relevant = append(relevant, d)
	}

	// Step 2: IoU matching (greedy)
	matched := make([]bool, len(relevant))
	objMatched := make([]bool, len(t.objects))

	for i, obj := range t.objects {
		bestIdx := -1
		bestIoU := t.cfg.IoUThreshold
		for j, d := range relevant {
			if matched[j] {
				continue
			}
			iou := computeIoU(obj.bbox, d.BBox)
			if iou > bestIoU {
				bestIoU = iou
				bestIdx = j
			}
		}
		if bestIdx >= 0 {
			matched[bestIdx] = true
			objMatched[i] = true
			t.updateMatched(obj, relevant[bestIdx])
		}
	}

	// Step 3: Update unmatched tracked objects
	var kept []*trackedObject
	for i, obj := range t.objects {
		if objMatched[i] {
			kept = append(kept, obj)
			continue
		}
		obj.missCount++
		obj.seenCount = 0
		switch obj.state {
		case trackTentative:
			// Never confirmed — discard
		case trackConfirmed:
			if obj.missCount >= t.cfg.LostFrames {
				obj.state = trackLost
			} else {
				kept = append(kept, obj)
			}
		case trackLost:
			// Already lost — discard
		}
	}

	// Step 4: Create new objects from unmatched detections
	for j, d := range relevant {
		if matched[j] {
			continue
		}
		tx, ty := targetPoint(d)
		t.nextID++
		kept = append(kept, &trackedObject{
			id:         t.nextID,
			label:      d.Label,
			bbox:       d.BBox,
			confidence: d.Confidence,
			state:      trackTentative,
			seenCount:  1,
			smoothX:    tx,
			smoothY:    ty,
			area:       bboxArea(d.BBox),
		})
	}
	t.objects = kept

	// Step 5: Target selection — cascade priority (face > head > body)
	var target *trackedObject
	targetPri := 0
	for _, obj := range t.objects {
		if obj.state != trackConfirmed {
			continue
		}
		p := labelPriority(obj.label)
		if p == 0 {
			continue
		}
		if p > targetPri || (p == targetPri && (target == nil || obj.area > target.area)) {
			target = obj
			targetPri = p
		}
	}

	if target == nil {
		return
	}

	// Step 6: Proportional control
	frameCX := float64(t.cfg.FrameWidth) / 2.0
	frameCY := float64(t.cfg.FrameHeight) / 2.0

	errX := target.smoothX - frameCX
	errY := target.smoothY - frameCY

	dzX := t.cfg.DeadZone * float64(t.cfg.FrameWidth)
	dzY := t.cfg.DeadZone * float64(t.cfg.FrameHeight)
	if math.Abs(errX) < dzX && math.Abs(errY) < dzY {
		return
	}

	gain := t.cfg.ProportionalGain
	deltaPan := -gain * errX
	deltaTilt := -gain * errY
	if t.cfg.InvertPan {
		deltaPan = -deltaPan
	}
	if t.cfg.InvertTilt {
		deltaTilt = -deltaTilt
	}

	deltaPan = clampF(deltaPan, -t.cfg.MaxDegPerFrame, t.cfg.MaxDegPerFrame)
	deltaTilt = clampF(deltaTilt, -t.cfg.MaxDegPerFrame, t.cfg.MaxDegPerFrame)

	newPan := clampF(t.currentPan+deltaPan, 0, 180)
	newTilt := clampF(t.currentTilt+deltaTilt, 0, 180)

	panInt := int(math.Round(newPan))
	tiltInt := int(math.Round(newTilt))
	if panInt == int(math.Round(t.currentPan)) && tiltInt == int(math.Round(t.currentTilt)) {
		t.currentPan = newPan
		t.currentTilt = newTilt
		return
	}

	t.currentPan = newPan
	t.currentTilt = newTilt

	go func() {
		if err := t.servo.SendServo(panInt, tiltInt); err != nil {
			t.logger.Debug("サーボコマンド送信失敗", "error", err)
		}
	}()
}

func (t *ObjectTracker) updateMatched(obj *trackedObject, d detect.Detection) {
	obj.bbox = d.BBox
	obj.confidence = d.Confidence
	obj.label = d.Label
	obj.area = bboxArea(d.BBox)
	obj.missCount = 0
	obj.seenCount++

	if obj.state == trackTentative && obj.seenCount >= t.cfg.ConfirmFrames {
		obj.state = trackConfirmed
		t.logger.Info("トラッキング対象を確定", "id", obj.id, "label", obj.label)
	} else if obj.state == trackLost {
		obj.state = trackConfirmed
		obj.seenCount = t.cfg.ConfirmFrames
	}

	tx, ty := targetPoint(d)
	a := t.cfg.SmoothingAlpha
	obj.smoothX = a*tx + (1-a)*obj.smoothX
	obj.smoothY = a*ty + (1-a)*obj.smoothY
}

// --- helpers ---

func computeIoU(a, b [4]int) float64 {
	x1 := max(a[0], b[0])
	y1 := max(a[1], b[1])
	x2 := min(a[2], b[2])
	y2 := min(a[3], b[3])
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	inter := float64((x2 - x1) * (y2 - y1))
	areaA := float64((a[2] - a[0]) * (a[3] - a[1]))
	areaB := float64((b[2] - b[0]) * (b[3] - b[1]))
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func bboxCenter(b [4]int) (float64, float64) {
	return float64(b[0]+b[2]) / 2.0, float64(b[1]+b[3]) / 2.0
}

func bboxArea(b [4]int) int {
	w := b[2] - b[0]
	h := b[3] - b[1]
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
