package device

// DeviceAdapter adapts Hub to consumer-side interfaces defined in capability/vision.
// It satisfies both servoCommander (for tracker) and deviceCommander (for tools).
type DeviceAdapter struct {
	hub *Hub
}

// NewDeviceAdapter creates a DeviceAdapter for the given Hub.
func NewDeviceAdapter(hub *Hub) *DeviceAdapter {
	return &DeviceAdapter{hub: hub}
}

// SendServo sends a servo command to the connected ESP device.
// Satisfies vision.servoCommander.
func (a *DeviceAdapter) SendServo(pan, tilt int) error {
	dev := a.hub.Device()
	if dev == nil {
		return nil
	}
	return dev.SendCommand(map[string]any{
		"cmd":  "servo",
		"pan":  pan,
		"tilt": tilt,
	})
}

// SendDeviceCommand sends a command to the first connected ESP device.
// Satisfies vision.deviceCommander.
func (a *DeviceAdapter) SendDeviceCommand(cmd map[string]any) error {
	dev := a.hub.Device()
	if dev == nil {
		return nil
	}
	return dev.SendCommand(cmd)
}

// BroadcastCommand sends a command to all connected clients.
// Satisfies vision.deviceCommander.
func (a *DeviceAdapter) BroadcastCommand(cmd map[string]any) error {
	return a.hub.BroadcastCommand(cmd)
}

// IsDeviceConnected returns true if an ESP device is connected.
// Satisfies vision.deviceCommander.
func (a *DeviceAdapter) IsDeviceConnected() bool {
	return a.hub.IsConnected()
}
