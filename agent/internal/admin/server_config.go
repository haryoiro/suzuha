package admin

// ServerConfig は管理ダッシュボードの設定を保持する。
type ServerConfig struct {
	Addr            string
	AgentMetrics    string
	AgentLogs       string
	AgentContext    string
	ConsolLogs      string
	ConsolidatorAPI string
	StaticDir       string
	PromptDir       string
	AuthUsername     string
	AuthPassword    string
}
