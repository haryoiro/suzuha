package mcpbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager manages MCP server connections and registers discovered tools.
type Manager struct {
	logger   *slog.Logger
	sessions []*mcp.ClientSession
}

// New creates a new MCP bridge Manager.
func New(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Start connects to each configured MCP server and registers discovered tools.
// Failures are logged but do not prevent other servers from connecting.
func (m *Manager) Start(ctx context.Context, servers []config.ToolServer, registry *tool.Registry) {
	for _, srv := range servers {
		if srv.Type != "mcp" {
			continue
		}
		if err := m.connectServer(ctx, srv, registry); err != nil {
			m.logger.Warn("mcp server connection failed, skipping",
				"name", srv.Name, "error", err)
		}
	}
}

func (m *Manager) connectServer(ctx context.Context, srv config.ToolServer, registry *tool.Registry) error {
	if srv.Command == "" {
		return fmt.Errorf("mcp server %q: command is required", srv.Name)
	}

	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// Pipe stderr to our logger so MCP server errors are visible.
	cmd.Stderr = &logWriter{logger: m.logger, name: srv.Name}

	transport := &mcp.CommandTransport{Command: cmd}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "suzuha-agent",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	m.sessions = append(m.sessions, session)

	// Discover and register tools.
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		session.Close()
		return fmt.Errorf("list tools: %w", err)
	}

	for _, t := range result.Tools {
		prefixedName := srv.Name + "." + t.Name
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			m.logger.Warn("mcp tool schema marshal failed",
				"server", srv.Name, "tool", t.Name, "error", err)
			continue
		}

		mcpTool := &MCPTool{
			name:        prefixedName,
			mcpName:     t.Name,
			description: t.Description,
			inputSchema: schema,
			session:     session,
		}
		registry.Register(mcpTool)
		m.logger.Info("mcp tool registered",
			"server", srv.Name, "tool", prefixedName)
	}

	m.logger.Info("mcp server connected",
		"name", srv.Name, "tools", len(result.Tools))
	return nil
}

// Close terminates all MCP sessions and their subprocesses.
func (m *Manager) Close() {
	for _, s := range m.sessions {
		s.Close()
	}
	m.sessions = nil
}

// MCPTool adapts an MCP server tool to the tool.Tool interface.
type MCPTool struct {
	name        string // prefixed: "server.toolname"
	mcpName     string // original MCP tool name
	description string
	inputSchema json.RawMessage
	session     *mcp.ClientSession
}

func (t *MCPTool) Name() string                { return t.name }
func (t *MCPTool) Description() string          { return t.description }
func (t *MCPTool) InputSchema() json.RawMessage { return t.inputSchema }

func (t *MCPTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return tool.ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
		}
	}

	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.mcpName,
		Arguments: args,
	})
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("mcp call failed: %v", err)), nil
	}

	return ConvertResult(res), nil
}

// ConvertResult converts an MCP CallToolResult to a tool.ToolResult.
func ConvertResult(res *mcp.CallToolResult) *tool.ToolResult {
	var contents []tool.Content
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			contents = append(contents, tool.Content{Type: "text", Text: v.Text})
		case *mcp.ImageContent:
			contents = append(contents, tool.Content{Type: "text", Text: "[image: " + v.MIMEType + "]"})
		case *mcp.AudioContent:
			contents = append(contents, tool.Content{Type: "text", Text: "[audio: " + v.MIMEType + "]"})
		default:
			contents = append(contents, tool.Content{Type: "text", Text: "[unsupported content type]"})
		}
	}
	if len(contents) == 0 {
		contents = []tool.Content{{Type: "text", Text: "(empty result)"}}
	}
	return &tool.ToolResult{Content: contents, IsError: res.IsError}
}

// logWriter adapts slog for use as an io.Writer (for MCP subprocess stderr).
type logWriter struct {
	logger *slog.Logger
	name   string
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.logger.Debug("mcp server stderr", "server", w.name, "output", string(p))
	return len(p), nil
}
