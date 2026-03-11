package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverEntry holds a connected MCP server session and its registered tool names.
type serverEntry struct {
	session   *mcpsdk.ClientSession
	toolNames []string // prefixed names: ["server_tool1", "server_tool2"]
}

// Manager manages MCP server connections and registers discovered tools.
type Manager struct {
	mu       sync.Mutex
	logger   *slog.Logger
	registry *tool.Registry
	servers  map[string]*serverEntry
}

// NewManager creates a new MCP bridge Manager.
func NewManager(logger *slog.Logger, registry *tool.Registry) *Manager {
	return &Manager{
		logger:   logger,
		registry: registry,
		servers:  make(map[string]*serverEntry),
	}
}

// Start connects to each configured MCP server and registers discovered tools.
// Failures are logged but do not prevent other servers from connecting.
func (m *Manager) Start(ctx context.Context, servers []config.ToolServer) {
	for _, srv := range servers {
		if srv.Type != "mcp" {
			continue
		}
		if _, err := m.ConnectServer(ctx, srv); err != nil {
			m.logger.Warn("MCPサーバー接続に失敗、スキップします",
				"name", srv.Name, "error", err)
		}
	}
}

// ConnectServer connects to an MCP server and registers its tools.
// Returns the list of registered tool names.
func (m *Manager) ConnectServer(ctx context.Context, srv config.ToolServer) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[srv.Name]; exists {
		return nil, fmt.Errorf("サーバー %q は既に接続されています", srv.Name)
	}

	if srv.Command == "" {
		return nil, fmt.Errorf("MCPサーバー %q: コマンドが必要です", srv.Name)
	}

	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "NO_COLOR=1", "FORCE_COLOR=0", "NODE_NO_WARNINGS=1")
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = &logWriter{logger: m.logger, name: srv.Name}

	// Create pipes manually so we can filter non-JSON lines from stdout.
	// Some MCP servers incorrectly write log messages to stdout, which
	// corrupts the JSON-RPC stream.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("標準入力パイプの作成に失敗: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("標準出力パイプの作成に失敗: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("コマンドの起動に失敗: %w", err)
	}

	// Wrap stdout with a filter that only passes JSON lines.
	filteredReader := &jsonLineReader{
		r:      bufio.NewReader(stdoutPipe),
		closer: stdoutPipe,
		logger: m.logger,
		name:   srv.Name,
	}

	transport := &mcpsdk.IOTransport{
		Reader: filteredReader,
		Writer: stdinPipe,
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "suzuha-agent",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("接続に失敗: %w", err)
	}

	// Discover and register tools.
	result, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("ツール一覧の取得に失敗: %w", err)
	}

	var toolNames []string
	for _, t := range result.Tools {
		prefixedName := srv.Name + "_" + t.Name
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			m.logger.Warn("MCPツールスキーマのマーシャルに失敗",
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
		m.registry.Register(mcpTool)
		toolNames = append(toolNames, prefixedName)
		m.logger.Info("MCPツールを登録",
			"server", srv.Name, "tool", prefixedName)
	}

	m.servers[srv.Name] = &serverEntry{
		session:   session,
		toolNames: toolNames,
	}

	m.logger.Info("MCPサーバーに接続",
		"name", srv.Name, "tools", len(toolNames))
	return toolNames, nil
}

// DisconnectServer stops an MCP server and unregisters its tools.
func (m *Manager) DisconnectServer(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("サーバー %q は接続されていません", name)
	}

	entry.session.Close()
	for _, toolName := range entry.toolNames {
		m.registry.Unregister(toolName)
	}
	delete(m.servers, name)

	m.logger.Info("MCPサーバーを切断", "name", name)
	return nil
}

// ServerToolNames returns the registered tool names for a connected server.
func (m *Manager) ServerToolNames(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.servers[name]
	if !ok {
		return nil
	}
	out := make([]string, len(entry.toolNames))
	copy(out, entry.toolNames)
	return out
}

// IsConnected returns true if the named server is connected.
func (m *Manager) IsConnected(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.servers[name]
	return ok
}

// Close terminates all MCP sessions and their subprocesses.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, entry := range m.servers {
		entry.session.Close()
		for _, toolName := range entry.toolNames {
			m.registry.Unregister(toolName)
		}
		delete(m.servers, name)
	}
}

// jsonLineReader filters an io.Reader to only pass through lines that start
// with '{' or '[' (JSON-RPC messages). Non-JSON lines (log output, ANSI codes)
// are silently discarded.
type jsonLineReader struct {
	r      *bufio.Reader
	closer io.Closer
	buf    bytes.Buffer
	logger *slog.Logger
	name   string
}

func (f *jsonLineReader) Read(p []byte) (int, error) {
	for f.buf.Len() == 0 {
		line, err := f.r.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
				f.buf.Write(line)
			} else if len(trimmed) > 0 {
				f.logger.Debug("MCP標準出力をフィルタ", "server", f.name, "line", string(trimmed))
			}
		}
		if err != nil {
			if f.buf.Len() > 0 {
				break
			}
			return 0, err
		}
	}
	return f.buf.Read(p)
}

func (f *jsonLineReader) Close() error {
	return f.closer.Close()
}

// MCPTool adapts an MCP server tool to the tool.Tool interface.
type MCPTool struct {
	name        string // prefixed: "server_toolname"
	mcpName     string // original MCP tool name
	description string
	inputSchema json.RawMessage
	session     *mcpsdk.ClientSession
}

func (t *MCPTool) Name() string                { return t.name }
func (t *MCPTool) Description() string          { return t.description }
func (t *MCPTool) InputSchema() json.RawMessage { return t.inputSchema }

func (t *MCPTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return tool.ErrorResult(fmt.Sprintf("無効な入力: %v", err)), nil
		}
	}

	res, err := t.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      t.mcpName,
		Arguments: args,
	})
	if err != nil {
		return tool.ErrorResult(fmt.Sprintf("MCP呼び出しに失敗: %v", err)), nil
	}

	return ConvertResult(res), nil
}

// ConvertResult converts an MCP CallToolResult to a tool.ToolResult.
func ConvertResult(res *mcpsdk.CallToolResult) *tool.ToolResult {
	var contents []tool.Content
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcpsdk.TextContent:
			contents = append(contents, tool.Content{Type: "text", Text: v.Text})
		case *mcpsdk.ImageContent:
			contents = append(contents, tool.Content{Type: "text", Text: "[image: " + v.MIMEType + "]"})
		case *mcpsdk.AudioContent:
			contents = append(contents, tool.Content{Type: "text", Text: "[audio: " + v.MIMEType + "]"})
		default:
			contents = append(contents, tool.Content{Type: "text", Text: "[サポートされていないコンテンツタイプ]"})
		}
	}
	if len(contents) == 0 {
		contents = []tool.Content{{Type: "text", Text: "(空の結果)"}}
	}
	return &tool.ToolResult{Content: contents, IsError: res.IsError}
}

// logWriter adapts slog for use as an io.Writer (for MCP subprocess stderr).
type logWriter struct {
	logger *slog.Logger
	name   string
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.logger.Debug("MCPサーバー標準エラー出力", "server", w.name, "output", string(p))
	return len(p), nil
}
