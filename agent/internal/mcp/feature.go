package mcp

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

// BootstrapStore は mcpapps ストアを生成して Setup を実行する。
func BootstrapStore(ctx context.Context, db *sql.DB) (*AppStore, error) {
	store := NewAppStore(db)
	if err := store.Setup(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// ReconnectEnabled は有効化されている App を MCP マネージャに再接続する。
// 起動時に 1 回呼ぶ想定。
func ReconnectEnabled(ctx context.Context, mgr *Manager, store *AppStore, logger *slog.Logger) {
	apps, err := store.ListEnabled(ctx)
	if err != nil {
		logger.Warn("mcpapps: 有効なアプリの一覧取得に失敗", "error", err)
		return
	}
	for _, app := range apps {
		srv := app.ToToolServer()
		connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		toolNames, err := mgr.ConnectServer(connectCtx, srv)
		cancel()
		if err != nil {
			logger.Warn("mcpapps: 再接続失敗", "app", app.Name, "error", err)
			continue
		}
		logger.Info("mcpapps: 再接続完了", "app", app.Name, "tools", len(toolNames))
	}
}

// NewTools は mcpapps 用のエージェントツール群を返す。
func NewTools(mgr *Manager, store *AppStore, logger *slog.Logger) []tool.Tool {
	registry := NewRegistryClient()
	return []tool.Tool{
		NewSearchTool(registry),
		NewInstallTool(store, mgr, registry, logger),
		NewUninstallTool(store, mgr),
		NewListAppsTool(store, mgr),
	}
}
