package remote

import (
	"context"
	"encoding/json"

	"github.com/haryoiro/suzuha/internal/tool"
)

// ProxyTool wraps a remote tool as a local tool.Tool implementation.
type ProxyTool struct {
	name        string
	description string
	inputSchema json.RawMessage
	client      *Client
}

func (p *ProxyTool) Name() string                { return p.name }
func (p *ProxyTool) Description() string         { return p.description }
func (p *ProxyTool) InputSchema() json.RawMessage { return p.inputSchema }

func (p *ProxyTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	return p.client.call(ctx, p.name, input)
}

var _ tool.Tool = (*ProxyTool)(nil)
