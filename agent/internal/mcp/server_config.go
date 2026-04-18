package mcp

// ServerConfig はリモートツールサーバーの接続設定を保持する。
type ServerConfig struct {
	Name      string
	Type      string // "websocket" or "mcp"
	Transport string // "stdio", "http" (for mcp type)
	URL       string
	Command   string
	Args      []string
	Env       map[string]string
}
