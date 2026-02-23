package transport

import (
	"context"
	"encoding/json"
)

// Transport abstracts the communication layer with tool servers.
// Implementations include WebSocket (native) and MCP bridge transports.
type Transport interface {
	Connect(ctx context.Context) error
	Send(ctx context.Context, msg *JsonRpcMessage) error
	Receive(ctx context.Context) (*JsonRpcMessage, error)
	Close() error
}

// JsonRpcMessage is a JSON-RPC 2.0 message.
type JsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

// JsonRpcError is a JSON-RPC 2.0 error object.
type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// NewRequest creates a JSON-RPC request message.
func NewRequest(id any, method string, params any) (*JsonRpcMessage, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return &JsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}

// IsError returns true if the message is an error response.
func (m *JsonRpcMessage) IsError() bool {
	return m.Error != nil
}

// IsNotification returns true if the message is a notification (no ID).
func (m *JsonRpcMessage) IsNotification() bool {
	return m.ID == nil && m.Method != ""
}
