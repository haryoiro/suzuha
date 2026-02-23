package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

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
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	); err != nil {
		return fmt.Errorf("memory: fts insert: %w", err)
	}

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
	// trigram tokenizer requires 3+ chars for substring match.
	// Fall back to LIKE for short queries.
	var q string
	var args []any

	if len([]rune(query)) >= 3 {
		q = `SELECT m.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		     FROM memories m
		     JOIN memories_fts f ON f.rowid = m.rowid
		     WHERE memories_fts MATCH ?`
		args = []any{query}
	} else {
		q = `SELECT m.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		     FROM memories m
		     WHERE m.content LIKE ?`
		args = []any{"%" + query + "%"}
	}

	if memType != "" {
		q += ` AND m.type = ?`
		args = append(args, string(memType))
	}

	if len([]rune(query)) >= 3 {
		q += ` ORDER BY rank LIMIT ?`
	} else {
		q += ` ORDER BY m.updated_at DESC LIMIT ?`
	}
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

func (s *SQLiteStore) List(ctx context.Context, opts ListOpts) ([]Memory, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.OrderBy == "" {
		opts.OrderBy = "updated_at"
	}
	if opts.OrderDir == "" {
		opts.OrderDir = "desc"
	}

	// Validate order fields to prevent SQL injection.
	switch opts.OrderBy {
	case "created_at", "updated_at":
	default:
		opts.OrderBy = "updated_at"
	}
	switch opts.OrderDir {
	case "asc", "desc":
	default:
		opts.OrderDir = "desc"
	}

	where := "1=1"
	var args []any

	if opts.Type != "" {
		where += " AND m.type = ?"
		args = append(args, string(opts.Type))
	}

	if opts.Query != "" {
		where += " AND m.rowid IN (SELECT rowid FROM memories_fts WHERE memories_fts MATCH ?)"
		args = append(args, opts.Query)
	}

	// Count total.
	var total int
	countQ := fmt.Sprintf("SELECT count(*) FROM memories m WHERE %s", where)
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("memory: list count: %w", err)
	}

	// Fetch page.
	q := fmt.Sprintf(
		"SELECT m.id, m.type, m.content, m.metadata, m.created_at, m.updated_at FROM memories m WHERE %s ORDER BY m.%s %s LIMIT ? OFFSET ?",
		where, opts.OrderBy, opts.OrderDir,
	)
	pageArgs := make([]any, len(args), len(args)+2)
	copy(pageArgs, args)
	pageArgs = append(pageArgs, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, q, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		var m Memory
		var metaJSON string
		var typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("memory: list scan: %w", err)
		}
		m.Type = MemoryType(typeStr)
		_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
		results = append(results, m)
	}
	return results, total, rows.Err()
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Memory, error) {
	var m Memory
	var metaJSON string
	var typeStr string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, type, content, metadata, created_at, updated_at FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("memory: get: %w", err)
	}
	m.Type = MemoryType(typeStr)
	_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
	return &m, nil
}

func (s *SQLiteStore) Update(ctx context.Context, mem *Memory) error {
	mem.UpdatedAt = time.Now()

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: marshal metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE memories SET type = ?, content = ?, metadata = ?, updated_at = ? WHERE id = ?`,
		string(mem.Type), mem.Content, string(metadataJSON), mem.UpdatedAt, mem.ID,
	)
	if err != nil {
		return fmt.Errorf("memory: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory: not found: %s", mem.ID)
	}

	// Rebuild FTS index for this row.
	_, _ = tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID)
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content)

	return tx.Commit()
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete from FTS.
	_, _ = tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, id)
	// Delete from vec.
	_, _ = tx.ExecContext(ctx,
		`DELETE FROM memories_vec WHERE id = ?`, id)
	// Delete from main table.
	res, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("memory: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory: not found: %s", id)
	}

	return tx.Commit()
}

// DB returns the underlying *sql.DB for sharing with other stores (e.g. user.Store).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
