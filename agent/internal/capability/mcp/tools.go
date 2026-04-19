package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/haryoiro/suzuha/internal/port/tool"
)

// SearchTool は MCP レジストリからサーバーを検索するツール。
type SearchTool struct {
	registry *RegistryClient
}

// NewSearchTool は SearchTool のインスタンスを生成する。
func NewSearchTool(reg *RegistryClient) *SearchTool {
	return &SearchTool{registry: reg}
}

func (t *SearchTool) Name() string   { return "search_mcp_apps" }
func (t *SearchTool) ReadOnly() bool { return true }
func (t *SearchTool) Description() string {
	return `Search the MCP Registry for available MCP server apps. Returns a list of servers matching the query with their name, description, and required environment variables. Use this to discover new tools you can install.`
}

func (t *SearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query (e.g. 'weather', 'browser', 'database')"},
			"limit": {"type": "integer", "description": "Max results (1-20, default 5)"}
		},
		"required": ["query"]
	}`)
}

func (t *SearchTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Query == "" {
		return tool.ErrorResult("クエリは必須です"), nil
	}
	if in.Limit <= 0 {
		in.Limit = 5
	}

	result, err := t.registry.Search(ctx, in.Query, in.Limit)
	if err != nil {
		return tool.ErrorResult("検索失敗: " + err.Error()), nil
	}

	if len(result.Servers) == 0 {
		return tool.TextResult("該当するMCPサーバーが見つかりません: " + in.Query), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d件のMCPサーバーが見つかりました:\n\n", len(result.Servers))

	for _, sr := range result.Servers {
		s := sr.Server
		title := s.Title
		if title == "" {
			title = s.Name
		}
		fmt.Fprintf(&sb, "### %s\n", title)
		fmt.Fprintf(&sb, "- **Name**: `%s`\n", s.Name)
		if s.Description != "" {
			fmt.Fprintf(&sb, "- **Description**: %s\n", s.Description)
		}

		// Show available packages.
		for _, pkg := range s.Packages {
			fmt.Fprintf(&sb, "- **Package**: %s (%s, %s)\n", pkg.Identifier, pkg.RegistryType, pkg.Transport.Type)

			// Show required env vars.
			var required []string
			for _, ev := range pkg.EnvironmentVariables {
				if ev.IsRequired {
					desc := ev.Name
					if ev.Description != "" {
						desc += " — " + ev.Description
					}
					required = append(required, desc)
				}
			}
			if len(required) > 0 {
				fmt.Fprintf(&sb, "- **Required env vars**: %s\n", strings.Join(required, "; "))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("インストールするには、サーバー名を指定して install_mcp_app を使用してください。")
	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*SearchTool)(nil)

// InstallTool は MCP サーバーアプリをレジストリからインストールするツール。
type InstallTool struct {
	store    *AppStore
	mcpMgr   *Manager
	registry *RegistryClient
	logger   *slog.Logger
}

// NewInstallTool は InstallTool のインスタンスを生成する。
func NewInstallTool(store *AppStore, mcpMgr *Manager, reg *RegistryClient, logger *slog.Logger) *InstallTool {
	return &InstallTool{store: store, mcpMgr: mcpMgr, registry: reg, logger: logger}
}

func (t *InstallTool) Name() string   { return "install_mcp_app" }
func (t *InstallTool) ReadOnly() bool { return false }
func (t *InstallTool) Description() string {
	return `Install an MCP server app from the registry. The server will be started immediately and its tools will become available. The installation is persisted and survives restarts. Use search_mcp_apps first to find the server name.`
}

func (t *InstallTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Server name from the registry (e.g. 'io.github.user/mcp-server-weather')"},
			"env":  {"type": "object", "description": "Environment variables the server needs (e.g. {\"API_KEY\": \"abc123\"})"}
		},
		"required": ["name"]
	}`)
}

func (t *InstallTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Name string            `json:"name"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Name == "" {
		return tool.ErrorResult("名前は必須です"), nil
	}

	// Fetch server details from registry.
	srv, err := t.registry.GetServer(ctx, in.Name)
	if err != nil {
		return tool.ErrorResult("レジストリ検索失敗: " + err.Error()), nil
	}

	// Select the best package.
	pkg, err := SelectPackage(*srv)
	if err != nil {
		return tool.ErrorResult(err.Error()), nil
	}

	// Convert to ToolServer config.
	toolSrv, err := ToToolServer(in.Name, pkg, in.Env)
	if err != nil {
		return tool.ErrorResult(err.Error()), nil
	}

	// Check if already connected.
	if t.mcpMgr.IsConnected(toolSrv.Name) {
		return tool.ErrorResult(fmt.Sprintf("サーバー %q は既にインストールされています", toolSrv.Name)), nil
	}

	// Pre-install the npm package globally so npx won't output download
	// progress to stdout (which corrupts the MCP JSON-RPC stream).
	if pkg.RegistryType == "npm" {
		pkgSpec := pkg.Identifier
		if pkg.Version != "" {
			pkgSpec += "@" + pkg.Version
		}
		t.logger.Info("mcpapps: npmパッケージを事前インストール中", "package", pkgSpec)
		installCmd := exec.CommandContext(ctx, "npm", "install", "-g", pkgSpec)
		installCmd.Env = append(os.Environ(), "NO_COLOR=1")
		if out, err := installCmd.CombinedOutput(); err != nil {
			return tool.ErrorResult(fmt.Sprintf("npmインストール失敗: %v\n%s", err, string(out))), nil
		}
	}

	// Connect and register tools.
	toolNames, err := t.mcpMgr.ConnectServer(ctx, toolSrv)
	if err != nil {
		return tool.ErrorResult("MCPサーバー接続失敗: " + err.Error()), nil
	}

	// Persist to DB.
	app := &App{
		Name:         toolSrv.Name,
		Title:        srv.Title,
		Description:  srv.Description,
		Version:      srv.Version,
		RegistryType: pkg.RegistryType,
		Identifier:   pkg.Identifier,
		Command:      toolSrv.Command,
		Args:         toolSrv.Args,
		Env:          toolSrv.Env,
		Transport:    toolSrv.Transport,
		Enabled:      true,
	}
	if err := t.store.Add(ctx, app); err != nil {
		// Rollback: disconnect on DB failure.
		t.mcpMgr.DisconnectServer(toolSrv.Name)
		return tool.ErrorResult("アプリ保存失敗: " + err.Error()), nil
	}

	title := srv.Title
	if title == "" {
		title = in.Name
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** をインストールしました!\n\n", title)
	fmt.Fprintf(&sb, "利用可能なツール (%d):\n", len(toolNames))
	for _, name := range toolNames {
		fmt.Fprintf(&sb, "- `%s`\n", name)
	}
	sb.WriteString("\nこれらのツールが利用可能になりました。")

	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*InstallTool)(nil)

// UninstallTool はインストール済みの MCP アプリをアンインストールするツール。
type UninstallTool struct {
	store  *AppStore
	mcpMgr *Manager
}

// NewUninstallTool は UninstallTool のインスタンスを生成する。
func NewUninstallTool(store *AppStore, mcpMgr *Manager) *UninstallTool {
	return &UninstallTool{store: store, mcpMgr: mcpMgr}
}

func (t *UninstallTool) Name() string   { return "uninstall_mcp_app" }
func (t *UninstallTool) ReadOnly() bool { return false }
func (t *UninstallTool) Description() string {
	return "Uninstall a previously installed MCP app. Stops the server and removes all its tools."
}

func (t *UninstallTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Name of the installed app to remove (use list_mcp_apps to see installed apps)"}
		},
		"required": ["name"]
	}`)
}

func (t *UninstallTool) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("無効な入力: " + err.Error()), nil
	}
	if in.Name == "" {
		return tool.ErrorResult("名前は必須です"), nil
	}

	// Disconnect server (ignore error if not connected -- may have crashed).
	t.mcpMgr.DisconnectServer(in.Name)

	// Remove from DB.
	if err := t.store.Remove(ctx, in.Name); err != nil {
		return tool.ErrorResult("アプリ削除失敗: " + err.Error()), nil
	}

	return tool.TextResult(fmt.Sprintf("アプリ %q をアンインストールしました。", in.Name)), nil
}

var _ tool.Tool = (*UninstallTool)(nil)

// ListAppsTool はインストール済みの MCP アプリ一覧を表示するツール。
type ListAppsTool struct {
	store  *AppStore
	mcpMgr *Manager
}

// NewListAppsTool は ListAppsTool のインスタンスを生成する。
func NewListAppsTool(store *AppStore, mcpMgr *Manager) *ListAppsTool {
	return &ListAppsTool{store: store, mcpMgr: mcpMgr}
}

func (t *ListAppsTool) Name() string   { return "list_mcp_apps" }
func (t *ListAppsTool) ReadOnly() bool { return true }
func (t *ListAppsTool) Description() string {
	return "List all installed MCP apps and their available tools."
}

func (t *ListAppsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (t *ListAppsTool) Execute(ctx context.Context, _ json.RawMessage) (*tool.ToolResult, error) {
	apps, err := t.store.List(ctx)
	if err != nil {
		return tool.ErrorResult("アプリ一覧取得失敗: " + err.Error()), nil
	}

	if len(apps) == 0 {
		return tool.TextResult("MCPアプリはインストールされていません。search_mcp_apps で検索し、install_mcp_app でインストールしてください。"), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "インストール済みMCPアプリ (%d):\n\n", len(apps))

	for _, app := range apps {
		title := app.Title
		if title == "" {
			title = app.Name
		}
		status := "接続中"
		if !t.mcpMgr.IsConnected(app.Name) {
			status = "切断"
		}

		fmt.Fprintf(&sb, "### %s [%s]\n", title, status)
		fmt.Fprintf(&sb, "- **Name**: `%s`\n", app.Name)
		if app.Description != "" {
			fmt.Fprintf(&sb, "- **Description**: %s\n", app.Description)
		}
		fmt.Fprintf(&sb, "- **Package**: %s (%s)\n", app.Identifier, app.RegistryType)

		toolNames := t.mcpMgr.ServerToolNames(app.Name)
		if len(toolNames) > 0 {
			fmt.Fprintf(&sb, "- **Tools**: %s\n", strings.Join(toolNames, ", "))
		}
		sb.WriteString("\n")
	}

	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*ListAppsTool)(nil)
