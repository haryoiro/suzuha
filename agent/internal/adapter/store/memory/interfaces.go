package memory

import (
	"context"
	"database/sql"
	"time"

	embedding "github.com/haryoiro/suzuha/internal/port/embedder"
)

// Store は long-term memory の主 storage interface。
// embedder.Part を使う SearchByParts を含むため、port/memory.Memory には
// 収まらない追加メソッドを持つ。embedder の port 整備後に
// Store = port/memory.Memory へ統合予定。
type Store interface {
	Save(ctx context.Context, mem *Memory) error
	Search(ctx context.Context, query string, limit int) ([]Memory, error)
	SearchWithContext(ctx context.Context, query string, limit int, filter SymbolicFilter) ([]Memory, error)
	SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error)
	SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]Memory, error)
	ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error)
	ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]Memory, error)
	ListByType(ctx context.Context, memType MemoryType, limit int) ([]Memory, error)
	ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error)
	ListRecent(ctx context.Context, since time.Time, limit int) ([]Memory, error)
	SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]Memory, error)
	IsDuplicate(ctx context.Context, content string, memType MemoryType) (dupID string, emb []float32, err error)
	IsDuplicateBatch(ctx context.Context, candidates []DupCandidate) ([]DupResult, error)
	Close() error
}

// AdminStore は admin dashboard が必要とする管理系メソッドを追加する。
type AdminStore interface {
	Store

	List(ctx context.Context, opts ListOpts) ([]Memory, int, error)
	Get(ctx context.Context, id string) (*Memory, error)
	Update(ctx context.Context, mem *Memory) error
	Delete(ctx context.Context, id string) error
	VecStats(ctx context.Context) (total, embedded int, err error)
	FindDuplicates(ctx context.Context, k int, threshold float64) ([]DuplicateGroup, error)
	DeleteBatch(ctx context.Context, ids []string) (int, error)
	SaveRaw(ctx context.Context, mem *Memory) error
	ListEmbeddedMemories(ctx context.Context) ([]Memory, error)
	ListAllEmbeddings(ctx context.Context) (map[string][]float32, error)

	// DB は生の *sql.DB を返す。typed メソッドへの移行が進んだら削除予定。
	DB() *sql.DB
}

// Backend は AdminStore + ライフサイクル管理を統合した interface。
type Backend interface {
	AdminStore
	SetMediaStore(ms MediaStore)
	SetOnSave(fn func())
	RunEmbeddingWorker(ctx context.Context)
	BackfillEmbeddings(ctx context.Context, batchSize int) (int, error)
}
