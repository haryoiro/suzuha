package vision

import "context"

// deviceCommander sends commands to the physical device.
// Defined here (consumer-side) to avoid importing the device package.
type deviceCommander interface {
	SendDeviceCommand(cmd map[string]any) error
	BroadcastCommand(cmd map[string]any) error
	IsDeviceConnected() bool
}

// VisionDescriber is the interface for describing images via VLM.
type VisionDescriber interface {
	HasVisionCapability() (available bool, inline bool)
	DescribeImage(ctx context.Context, imageURL string, prompt ...string) (string, error)
}
