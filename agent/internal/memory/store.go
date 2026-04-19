package memory

import (
	"context"
	"database/sql"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/embedder"
	"github.com/haryoiro/suzuha/internal/domain/memo"
)

// embedder 入力型の再エクスポート (SearchByParts で使う)。
type Part = embedding.Part
type Modality = embedding.Modality

const (
	ModalityText  = embedding.ModalityText
	ModalityImage = embedding.ModalityImage
	ModalityAudio = embedding.ModalityAudio
)

// domain/memo への型エイリアス群。既存呼び出し側は `memory.Memory` 等を
// そのまま使えるよう温存する。正準定義は domain/memo/。
type (
	MemoryType     = memo.MemoryType
	Attachment     = memo.Attachment
	Memory         = memo.Memory
	SymbolicFilter = memo.SymbolicFilter
	DupCandidate   = memo.DupCandidate
	DupResult      = memo.DupResult
	DuplicateGroup = memo.DuplicateGroup
	ListOpts       = memo.ListOpts
)

// MemoryType 定数の再エクスポート。
const (
	MemoryTypeUser    = memo.MemoryTypeUser
	MemoryTypeWorld   = memo.MemoryTypeWorld
	MemoryTypeTool    = memo.MemoryTypeTool
	MemoryTypeEpisode = memo.MemoryTypeEpisode
	MemoryTypeSelf    = memo.MemoryTypeSelf
	MemoryTypeMemo    = memo.MemoryTypeMemo
)

// MediaStore stores and retrieves binary media data.
type MediaStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// Store is the long-term memory storage interface.
// embedding.Part を使う SearchByParts を含むため、port/memory.Memory には
// 収まらない追加メソッドを持つ。Phase 8a で SearchByParts を port 化できる
// 見込みが立ったら Store = port/memory.Memory に統合する。
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

// AdminStore extends Store with methods needed by the admin dashboard.
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

	// DB returns the underlying *sql.DB for direct queries.
	// Deprecated: prefer using typed methods instead. Will be removed in a future phase.
	DB() *sql.DB
}

// Backend は AdminStore + ライフサイクル管理を統合したインターフェース。
type Backend interface {
	AdminStore
	SetMediaStore(ms MediaStore)
	SetOnSave(fn func())
	RunEmbeddingWorker(ctx context.Context)
	BackfillEmbeddings(ctx context.Context, batchSize int) (int, error)
}
