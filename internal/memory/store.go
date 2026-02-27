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
	MemoryTypeUser  MemoryType = "user"
	MemoryTypeWorld MemoryType = "world"
	MemoryTypeTool  MemoryType = "tool"
	MemoryTypeRSS   MemoryType = "rss"
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

	// Close releases database resources.
	Close() error
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

	// Delete removes a memory by ID from all tables.
	Delete(ctx context.Context, id string) error

	// DB returns the underlying *sql.DB for direct queries.
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
