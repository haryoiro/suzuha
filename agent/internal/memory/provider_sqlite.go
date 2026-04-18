//go:build sqlite

package memory

import (
	"log/slog"

	"github.com/haryoiro/suzuha/external/embedding"
)

// NewSQLiteBackend は SQLite バックエンドを生成する。
func NewSQLiteBackend(dbPath string, embedder embedding.Embedder, logger *slog.Logger) (Backend, error) {
	return NewSQLiteStore(dbPath, embedder, true, logger)
}
