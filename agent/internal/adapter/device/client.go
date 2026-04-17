package device

// Client is a connected display+speaker endpoint (ESP32 or browser).
type Client interface {
	ID() string
	Kind() string // "esp" or "web"
	SendCommand(cmd map[string]any) error
	SendTTS(pcm []byte) error
	Close() error
}
