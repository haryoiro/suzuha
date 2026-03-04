package memory

import (
	"context"
	"database/sql"
	"time"
)

// MemoryType categorizes memory entries.
type MemoryType string

// Memory type constants categorize long-term memory entries.
const (
	MemoryTypeUser    MemoryType = "user"
	MemoryTypeWorld   MemoryType = "world"
	MemoryTypeTool    MemoryType = "tool"
	MemoryTypeRSS     MemoryType = "rss"
	MemoryTypeEpisode MemoryType = "episode"
	MemoryTypeSelf    MemoryType = "self"
)

// Memory is a single long-term memory entry.
type Memory struct {
	ID        string         `json:"id"`
	Type      MemoryType     `json:"type"`
	Content   string         `json:"content"`
	Embedding []float32      `json:"embedding,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Store is the long-term memory storage interface.
type Store interface {
	// Save persists a memory entry. If the memory has an embedding,
	// it is stored for vector search. The content is indexed for FTS.
	Save(ctx context.Context, mem *Memory) error

	// Search performs hybrid search (vector similarity + FTS5 keyword)
	// and returns up to limit results merged via Reciprocal Rank Fusion.
	Search(ctx context.Context, query string, limit int) ([]Memory, error)

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

	// IsDuplicate checks if a very similar memory already exists.
	// Returns the existing memory ID if found, or empty string if no duplicate.
	IsDuplicate(ctx context.Context, content string, memType MemoryType) (string, error)

	// Close releases database resources.
	Close() error
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

	// DB returns the underlying *sql.DB for direct queries.
	// Deprecated: prefer using typed methods instead. Will be removed in a future phase.
	DB() *sql.DB
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

// EmbedFunc generates an embedding vector for the given text.
// It is injected from outside to avoid circular dependency with the llm package.
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)
