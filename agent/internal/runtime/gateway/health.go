package gateway

import "time"

// SourceState はソースの接続状態を表す。
type SourceState string

const (
	StateStarting SourceState = "starting"
	StateRunning  SourceState = "running"
	StateStopped  SourceState = "stopped"
	StateError    SourceState = "error"
)

// SourceStatus はソースの現在の健全性を保持する。
type SourceStatus struct {
	Name      string      `json:"name"`
	State     SourceState `json:"state"`
	StartedAt *time.Time  `json:"started_at,omitempty"`
	Error     string      `json:"error,omitempty"`
}
