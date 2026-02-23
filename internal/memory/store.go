package memory

import (
	"context"
	"time"
)

// MemoryType categorizes memory entries.
type MemoryType string

const (
	MemoryTypeUser  MemoryType = "user"
	MemoryTypeWorld MemoryType = "world"
	MemoryTypeTool  MemoryType = "tool"
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

	// Close releases database resources.
	Close() error
}

// EmbedFunc generates an embedding vector for the given text.
// It is injected from outside to avoid circular dependency with the llm package.
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)
