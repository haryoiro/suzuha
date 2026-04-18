//go:build !sqlite

package memory

import (
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
)

// NewSQLiteBackend は SQLite がビルドされていない場合にエラーを返す。
func NewSQLiteBackend(_ string, _ embedding.Embedder, _ *slog.Logger) (Backend, error) {
	return nil, fmt.Errorf("memory: SQLite サポートはビルドタグ 'sqlite' が必要です (postgres_url を設定してください)")
}
