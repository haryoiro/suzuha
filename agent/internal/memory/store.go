package memory

import (
	"context"
	"database/sql"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/embedder"
)

// Part は embedding.Part の型エイリアス。domain レイヤーが external/embedding を直接 import しなくて済むようにする。
type Part = embedding.Part

// Modality は embedding.Modality の型エイリアス。
type Modality = embedding.Modality

const (
	ModalityText  = embedding.ModalityText
	ModalityImage = embedding.ModalityImage
	ModalityAudio = embedding.ModalityAudio
)

// MemoryType categorizes memory entries.
type MemoryType string

// Memory type constants categorize long-term memory entries.
const (
	MemoryTypeUser    MemoryType = "user"
	MemoryTypeWorld   MemoryType = "world"
	MemoryTypeTool    MemoryType = "tool"
	MemoryTypeEpisode MemoryType = "episode"
	MemoryTypeSelf    MemoryType = "self"
	MemoryTypeMemo    MemoryType = "memo"
)

// Attachment is a reference to a media file stored in MediaStore.
type Attachment struct {
	Key      string `json:"key"`       // storage key (e.g. "memories/abc123/0.png")
	Modality string `json:"modality"`  // "image" or "audio"
	MimeType string `json:"mime_type"` // "image/png", "audio/wav", etc.
}

// Memory is a single long-term memory entry.
type Memory struct {
	ID          string         `json:"id"`
	Type        MemoryType     `json:"type"`
	Content     string         `json:"content"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Embedding   []float32      `json:"embedding,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`

	// 構造化フィールド — コンソリデーターが抽出し、検索・フィルタリングに使用する。
	Keywords  []string   `json:"keywords,omitempty"`   // 検索キーワード（名前、エンティティ、トピック語）
	Topic     string     `json:"topic,omitempty"`      // トピック分類（"技術/Go", "日常/食事"）
	Persons   []string   `json:"persons,omitempty"`    // 関連ユーザーID（user_id + participants を統合）
	EventTime *time.Time `json:"event_time,omitempty"` // イベント発生日時（CreatedAt とは異なる）
}

// MediaStore stores and retrieves binary media data.
type MediaStore interface {
	// Put stores data under the given key.
	Put(ctx context.Context, key string, data []byte) error

	// Get retrieves data by key. Returns os.ErrNotExist if not found.
	Get(ctx context.Context, key string) ([]byte, error)

	// Delete removes data by key. No error if not found.
	Delete(ctx context.Context, key string) error
}

// SymbolicFilter は Symbolic 検索（メタデータベース）の制約を指定する。
// 全フィールドはオプション。ゼロ値は「そのフィールドでフィルタしない」を意味する。
type SymbolicFilter struct {
	PersonIDs   []string  // persons JSON 配列にこれらの ID のいずれかが含まれるメモリにマッチ
	TopicPrefix string    // topic がこのプレフィックスで始まるメモリにマッチ（例: "技術"）
	Since       time.Time // event_time >= Since（event_time が NULL なら created_at で代替）
}

// IsEmpty は Symbolic フィルタが一切指定されていない場合に true を返す。
func (f SymbolicFilter) IsEmpty() bool {
	return len(f.PersonIDs) == 0 && f.TopicPrefix == "" && f.Since.IsZero()
}

// Store is the long-term memory storage interface.
type Store interface {
	// Save persists a memory entry. If the memory has an embedding,
	// it is stored for vector search. The content is indexed for FTS.
	Save(ctx context.Context, mem *Memory) error

	// Search performs hybrid search (vector similarity + FTS5 keyword)
	// and returns up to limit results merged via Reciprocal Rank Fusion.
	Search(ctx context.Context, query string, limit int) ([]Memory, error)

	// SearchWithContext は3軸ハイブリッド検索（FTS + Vec + Symbolic）を行う。
	// SymbolicFilter で指定された条件に合致するメモリを RRF でブーストする。
	SearchWithContext(ctx context.Context, query string, limit int, filter SymbolicFilter) ([]Memory, error)

	// SearchByType narrows search to a specific memory type.
	SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error)

	// SearchRecent performs hybrid search but only returns memories created after since.
	SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]Memory, error)

	// ListByUser returns user-type memories for a specific user ID (from metadata).
	ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error)

	// ListEpisodesByParticipant returns episode memories where userID appears
	// in metadata.participants. No embedding required.
	ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]Memory, error)

	// ListByType returns the most recent memories of a specific type, up to limit.
	ListByType(ctx context.Context, memType MemoryType, limit int) ([]Memory, error)

	// ListRecentByType returns memories of a specific type created after since, up to limit.
	ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error)

	// ListRecent は since 以降に作成された全タイプの最新メモリを limit 件まで返す。
	ListRecent(ctx context.Context, since time.Time, limit int) ([]Memory, error)

	// SearchByParts performs vector search using multimodal input (e.g. image).
	// The parts are embedded and used for KNN similarity search.
	SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]Memory, error)

	// IsDuplicate checks if a very similar memory already exists.
	// Returns the existing memory ID if found (or empty string), and the
	// computed embedding (which the caller can attach to the Memory before
	// Save to avoid a redundant backfill).
	IsDuplicate(ctx context.Context, content string, memType MemoryType) (dupID string, emb []float32, err error)

	// IsDuplicateBatch checks multiple candidates for duplicates in a single
	// batch, minimising embedding API calls.
	IsDuplicateBatch(ctx context.Context, candidates []DupCandidate) ([]DupResult, error)

	// Close releases database resources.
	Close() error
}

// DupCandidate is a single input for batch duplicate checking.
type DupCandidate struct {
	Content string
	Type    MemoryType
}

// DupResult is the output per candidate from IsDuplicateBatch.
type DupResult struct {
	DupID     string    // non-empty if a duplicate was found
	Embedding []float32 // computed embedding (nil if FTS pre-check caught it)
}

// DuplicateGroup is a cluster of similar memories found by vector distance.
type DuplicateGroup struct {
	Memories []Memory `json:"memories"`
}

// AdminStore extends Store with methods needed by the admin dashboard.
type AdminStore interface {
	Store

	// List returns memories with pagination and optional filtering.
	List(ctx context.Context, opts ListOpts) ([]Memory, int, error)

	// Get returns a single memory by ID.
	Get(ctx context.Context, id string) (*Memory, error)

	// Update updates an existing memory's content, type, and/or metadata.
	Update(ctx context.Context, mem *Memory) error

	// Delete removes a memory by ID from all tables (FTS, vec, main).
	Delete(ctx context.Context, id string) error

	// VecStats returns counts of total memories and vector-embedded memories.
	VecStats(ctx context.Context) (total, embedded int, err error)

	// FindDuplicates returns groups of semantically similar memories
	// using KNN vector search with the given number of neighbors per entry.
	FindDuplicates(ctx context.Context, k int, threshold float64) ([]DuplicateGroup, error)

	// DeleteBatch removes multiple memories by ID.
	// Returns the count of successfully deleted memories.
	DeleteBatch(ctx context.Context, ids []string) (int, error)

	// SaveRaw inserts a memory without generating an embedding.
	// Used for merge operations where embedding will be backfilled later.
	SaveRaw(ctx context.Context, mem *Memory) error

	// ListEmbeddedMemories は埋め込みベクトルを持つ全メモリを type, created_at 順で返す。
	// メンテナンスパイプラインで使用。
	ListEmbeddedMemories(ctx context.Context) ([]Memory, error)

	// ListAllEmbeddings は全埋め込みベクトルを ID をキーとした map で返す。
	// メンテナンスパイプラインでペアワイズ cosine distance 計算に使用。
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

// ListOpts controls pagination and filtering for List.
type ListOpts struct {
	Offset   int
	Limit    int
	Type     MemoryType // empty = all types
	Query    string     // FTS search query, empty = no filter
	OrderBy  string     // "created_at" | "updated_at", default "updated_at"
	OrderDir string     // "asc" | "desc", default "desc"
}
