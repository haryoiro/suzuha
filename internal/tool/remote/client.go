package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/haryoiro/suzuha/internal/transport"
)

// Client manages a connection to a remote tool server and exposes
// discovered tools as tool.Tool instances.
type Client struct {
	name      string
	transport transport.Transport
	logger    *slog.Logger

	mu    sync.RWMutex
	tools map[string]*ProxyTool

	nextID atomic.Int64
}

// NewClient creates a new remote tool client.
func NewClient(name string, t transport.Transport, logger *slog.Logger) *Client {
	return &Client{
		name:      name,
		transport: t,
		logger:    logger,
		tools:     make(map[string]*ProxyTool),
	}
}

// Connect establishes the transport connection and discovers available tools.
func (c *Client) Connect(ctx context.Context) error {
	if err := c.transport.Connect(ctx); err != nil {
		return fmt.Errorf("remote %s: connect: %w", c.name, err)
	}
	return c.discover(ctx)
}

// discover fetches the tool list from the remote server using JSON-RPC.
func (c *Client) discover(ctx context.Context) error {
	id := c.nextID.Add(1)
	req, err := transport.NewRequest(id, "tools/list", nil)
	if err != nil {
		return fmt.Errorf("remote %s: build discover request: %w", c.name, err)
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return fmt.Errorf("remote %s: send discover: %w", c.name, err)
	}

	resp, err := c.transport.Receive(ctx)
	if err != nil {
		return fmt.Errorf("remote %s: receive discover: %w", c.name, err)
	}

	if resp.IsError() {
		return fmt.Errorf("remote %s: discover error: %s", c.name, resp.Error.Message)
	}

	var result struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("remote %s: parse tool list: %w", c.name, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, td := range result.Tools {
		schema, _ := json.Marshal(td.InputSchema)
		c.tools[td.Name] = &ProxyTool{
			name:        td.Name,
			description: td.Description,
			inputSchema: schema,
			client:      c,
		}
		c.logger.Info("discovered remote tool", "server", c.name, "tool", td.Name)
	}
	return nil
}

// Tools returns all discovered tools.
func (c *Client) Tools() []tool.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]tool.Tool, 0, len(c.tools))
	for _, t := range c.tools {
		out = append(out, t)
	}
	return out
}

// call invokes a tool on the remote server via JSON-RPC.
func (c *Client) call(ctx context.Context, name string, input json.RawMessage) (*tool.ToolResult, error) {
	id := c.nextID.Add(1)
	req, err := transport.NewRequest(id, "tools/call", map[string]any{
		"name":  name,
		"input": input,
	})
	if err != nil {
		return nil, fmt.Errorf("remote %s: build call request: %w", c.name, err)
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("remote %s: send call: %w", c.name, err)
	}

	resp, err := c.transport.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("remote %s: receive call: %w", c.name, err)
	}

	if resp.IsError() {
		return tool.ErrorResult(resp.Error.Message), nil
	}

	var result tool.ToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("remote %s: parse result: %w", c.name, err)
	}
	return &result, nil
}

// Close shuts down the transport.
func (c *Client) Close() error {
	return c.transport.Close()
}

// toolDef is the JSON shape returned by tools/list.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}
