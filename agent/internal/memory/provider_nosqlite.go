//go:build !sqlite

package memory

import (
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
)

func newSQLiteBackend(_ string, _ embedding.Embedder, _ *slog.Logger) (Backend, error) {
	return nil, fmt.Errorf("memory: SQLite サポートはビルドタグ 'sqlite' が必要です (postgres_url を設定してください)")
}
