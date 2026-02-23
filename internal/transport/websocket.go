package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocketTransport implements Transport over WebSocket.
type WebSocketTransport struct {
	url  string
	conn *websocket.Conn
	mu   sync.Mutex // protects conn writes
}

// NewWebSocket creates a new WebSocket transport for the given URL.
func NewWebSocket(url string) *WebSocketTransport {
	return &WebSocketTransport{url: url}
}

func (t *WebSocketTransport) Connect(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.url, nil)
	if err != nil {
		return fmt.Errorf("websocket: connect %s: %w", t.url, err)
	}
	t.conn = conn
	return nil
}

func (t *WebSocketTransport) Send(ctx context.Context, msg *JsonRpcMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("websocket: marshal: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("websocket: write: %w", err)
	}
	return nil
}

func (t *WebSocketTransport) Receive(ctx context.Context) (*JsonRpcMessage, error) {
	// Set read deadline from context if available.
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetReadDeadline(deadline)
	}

	_, data, err := t.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("websocket: read: %w", err)
	}

	var msg JsonRpcMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("websocket: unmarshal: %w", err)
	}
	return &msg, nil
}

func (t *WebSocketTransport) Close() error {
	if t.conn == nil {
		return nil
	}
	return t.conn.Close()
}
