package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

// SQLiteStore implements Store using SQLite + sqlite-vec + FTS5.
type SQLiteStore struct {
	db      *sql.DB
	embedFn EmbedFunc
}

// NewSQLiteStore opens or creates a SQLite database at dbPath.
// If runMigrations is true, pending schema migrations are applied.
// Typically only the agent process should run migrations to avoid race conditions.
func NewSQLiteStore(dbPath string, embedFn EmbedFunc, runMigrations bool) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}

	// Enable WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: set WAL: %w", err)
	}

	if runMigrations {
		if err := migrate(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: migrate: %w", err)
		}
	}

	return &SQLiteStore{db: db, embedFn: embedFn}, nil
}

func (s *SQLiteStore) Save(ctx context.Context, mem *Memory) error {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := time.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: marshal metadata: %w", err)
	}

	// Generate embedding if not provided.
	if len(mem.Embedding) == 0 && s.embedFn != nil {
		emb, err := s.embedFn(ctx, mem.Content)
		if err != nil {
			// Non-fatal: save without embedding.
			emb = nil
		}
		mem.Embedding = emb
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Insert main record.
	_, err = tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		mem.CreatedAt, mem.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("memory: insert: %w", err)
	}

	// Update FTS index.
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	)

	// Update vector index.
	if len(mem.Embedding) > 0 {
		embJSON, _ := json.Marshal(mem.Embedding)
		_, _ = tx.ExecContext(ctx,
			`INSERT INTO memories_vec (id, embedding) VALUES (?, ?)`,
			mem.ID, string(embJSON),
		)
	}

	return tx.Commit()
}

func (s *SQLiteStore) Search(ctx context.Context, query string, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit)
}

func (s *SQLiteStore) SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, memType, limit)
}

func (s *SQLiteStore) searchInternal(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error) {
	// For now, use FTS5 keyword search only.
	// TODO: Add vector search + Reciprocal Rank Fusion when sqlite-vec is integrated.

	q := `SELECT m.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
	      FROM memories m
	      JOIN memories_fts f ON f.rowid = m.rowid
	      WHERE memories_fts MATCH ?`
	args := []any{query}

	if memType != "" {
		q += ` AND m.type = ?`
		args = append(args, string(memType))
	}
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search: %w", err)
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		var m Memory
		var metaJSON string
		var typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("memory: scan: %w", err)
		}
		m.Type = MemoryType(typeStr)
		_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
