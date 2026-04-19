// Package memory は長期記憶 (memo) の Service 契約を定義する。
// - Memory: agent pipeline から使う主 API (save / search / list)
// - Management: admin 管理画面から使う CRUD + 保守 API
// - Media: binary 添付ファイルの put/get
//
// 最終形の実装は capability/memory/ + adapter/store/memory/ に配置する想定。
// 暫定は internal/memory/*.DBStore がこれらの interface を満たす。
package memory

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	"github.com/haryoiro/suzuha/internal/port/embedder"
)

// Memory は agent pipeline が使う主 API。
type Memory interface {
	Save(ctx context.Context, mem *memo.Memory) error
	Search(ctx context.Context, query string, limit int) ([]memo.Memory, error)
	SearchWithContext(ctx context.Context, query string, limit int, filter memo.SymbolicFilter) ([]memo.Memory, error)
	SearchByType(ctx context.Context, query string, memType memo.MemoryType, limit int) ([]memo.Memory, error)
	SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]memo.Memory, error)
	SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]memo.Memory, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]memo.Memory, error)
	ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]memo.Memory, error)
	ListByType(ctx context.Context, memType memo.MemoryType, limit int) ([]memo.Memory, error)
	ListRecentByType(ctx context.Context, memType memo.MemoryType, since time.Time, limit int) ([]memo.Memory, error)
	ListRecent(ctx context.Context, since time.Time, limit int) ([]memo.Memory, error)
	IsDuplicate(ctx context.Context, content string, memType memo.MemoryType) (dupID string, emb []float32, err error)
	IsDuplicateBatch(ctx context.Context, candidates []memo.DupCandidate) ([]memo.DupResult, error)
	Close() error
}

// Management は admin dashboard が使う管理系 API。
// 名前衝突 (api/admin) 回避のため "Admin" ではなく Management を採用。
type Management interface {
	Memory

	List(ctx context.Context, opts memo.ListOpts) ([]memo.Memory, int, error)
	Get(ctx context.Context, id string) (*memo.Memory, error)
	Update(ctx context.Context, mem *memo.Memory) error
	Delete(ctx context.Context, id string) error
	VecStats(ctx context.Context) (total, embedded int, err error)
	FindDuplicates(ctx context.Context, k int, threshold float64) ([]memo.DuplicateGroup, error)
	DeleteBatch(ctx context.Context, ids []string) (int, error)
	SaveRaw(ctx context.Context, mem *memo.Memory) error
	ListEmbeddedMemories(ctx context.Context) ([]memo.Memory, error)
	ListAllEmbeddings(ctx context.Context) (map[string][]float32, error)
}

// Media は binary 添付ファイル (画像 / 音声) の put/get。
type Media interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}
