package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

func init() {
	sqlite_vec.Auto()
}

// SQLiteStore implements Store using SQLite + sqlite-vec + FTS5.
type SQLiteStore struct {
	db      *sql.DB
	embedFn EmbedFunc
	onSave  func() // optional hook called on successful Save
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

// SetOnSave registers a callback invoked after each successful Save.
func (s *SQLiteStore) SetOnSave(fn func()) { s.onSave = fn }

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
		if blob, err := sqlite_vec.SerializeFloat32(mem.Embedding); err == nil {
			_, _ = tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
				mem.ID, blob,
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if s.onSave != nil {
		s.onSave()
	}
	return nil
}

func (s *SQLiteStore) Search(ctx context.Context, query string, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, time.Time{})
}

func (s *SQLiteStore) SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, memType, limit, time.Time{})
}

func (s *SQLiteStore) SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, since)
}

// rrfK is the constant used in Reciprocal Rank Fusion scoring.
const rrfK = 60

// scoredID holds a memory ID with its vector distance from a KNN query.
type scoredID struct {
	id       string
	distance float32
}

func (s *SQLiteStore) searchInternal(ctx context.Context, query string, memType MemoryType, limit int, since time.Time) ([]Memory, error) {
	// 1. FTS keyword search.
	ftsResults, ftsErr := s.searchFTS(ctx, query, memType, limit*2, since)

	// 2. Vector similarity search (if embedFn is available).
	var vecResults []scoredID
	var vecErr error
	if s.embedFn != nil {
		vecResults, vecErr = s.searchVec(ctx, query, memType, limit*2)
		// Non-fatal: degrade to FTS-only on vec failure.
	}

	// Filter vec results by time if needed.
	if !since.IsZero() && len(vecResults) > 0 {
		vecResults, _ = s.filterVecBySince(ctx, vecResults, since, limit*2)
	}

	// 3. If both failed, return FTS error.
	if ftsErr != nil && (vecErr != nil || len(vecResults) == 0) {
		return nil, fmt.Errorf("memory: search: %w", ftsErr)
	}

	// 4. If only FTS succeeded, return FTS results.
	if len(vecResults) == 0 || vecErr != nil {
		return ftsResults, ftsErr
	}

	// 5. If only vec succeeded, load and return vec results.
	if ftsErr != nil || len(ftsResults) == 0 {
		ids := make([]string, len(vecResults))
		for i, v := range vecResults {
			ids[i] = v.id
		}
		loaded, err := s.loadMemoriesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		var results []Memory
		for _, v := range vecResults {
			if m, ok := loaded[v.id]; ok {
				results = append(results, m)
			}
			if len(results) >= limit {
				break
			}
		}
		return results, nil
	}

	// 6. Both succeeded: merge via RRF.
	return s.rrfMerge(ctx, ftsResults, vecResults, limit)
}

// searchFTS performs keyword search via FTS5 (trigram) or LIKE fallback.
func (s *SQLiteStore) searchFTS(ctx context.Context, query string, memType MemoryType, limit int, since time.Time) ([]Memory, error) {
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

	if !since.IsZero() {
		q += ` AND m.created_at >= ?`
		args = append(args, since)
	}

	if len([]rune(query)) >= 3 {
		q += ` ORDER BY rank LIMIT ?`
	} else {
		q += ` ORDER BY m.updated_at DESC LIMIT ?`
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: fts search: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// searchVec performs KNN vector search via sqlite-vec.
func (s *SQLiteStore) searchVec(ctx context.Context, query string, memType MemoryType, limit int) ([]scoredID, error) {
	embedding, err := s.embedFn(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("memory: embed query: %w", err)
	}
	if len(embedding) == 0 {
		return nil, nil
	}

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("memory: serialize embedding: %w", err)
	}

	// Over-fetch if we need to filter by type in Go.
	fetchLimit := limit
	if memType != "" {
		fetchLimit = limit * 3
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, distance FROM memories_vec WHERE embedding MATCH ? AND k = ?`,
		blob, fetchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: vec search: %w", err)
	}
	defer rows.Close()

	var results []scoredID
	for rows.Next() {
		var r scoredID
		if err := rows.Scan(&r.id, &r.distance); err != nil {
			return nil, fmt.Errorf("memory: vec scan: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter by type if needed.
	if memType != "" && len(results) > 0 {
		results, err = s.filterVecByType(ctx, results, memType, limit)
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// filterVecByType filters vec results by memory type using a DB lookup.
func (s *SQLiteStore) filterVecByType(ctx context.Context, results []scoredID, memType MemoryType, limit int) ([]scoredID, error) {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.id
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, string(memType))

	q := fmt.Sprintf(`SELECT id FROM memories WHERE id IN (%s) AND type = ?`, placeholders)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: filter by type: %w", err)
	}
	defer rows.Close()

	allowed := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var filtered []scoredID
	for _, r := range results {
		if allowed[r.id] {
			filtered = append(filtered, r)
		}
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

// filterVecBySince filters vec results by creation time.
func (s *SQLiteStore) filterVecBySince(ctx context.Context, results []scoredID, since time.Time, limit int) ([]scoredID, error) {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.id
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, since)

	q := fmt.Sprintf(`SELECT id FROM memories WHERE id IN (%s) AND created_at >= ?`, placeholders)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: filter by since: %w", err)
	}
	defer rows.Close()

	allowed := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var filtered []scoredID
	for _, r := range results {
		if allowed[r.id] {
			filtered = append(filtered, r)
		}
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

// rrfMerge combines FTS and vector search results using Reciprocal Rank Fusion.
func (s *SQLiteStore) rrfMerge(ctx context.Context, ftsResults []Memory, vecResults []scoredID, limit int) ([]Memory, error) {
	scores := make(map[string]float64)
	memMap := make(map[string]Memory)

	// FTS results contribute rank-based scores.
	for rank, m := range ftsResults {
		scores[m.ID] += 1.0 / float64(rrfK+rank+1)
		memMap[m.ID] = m
	}

	// Vec results contribute rank-based scores (already ordered by distance asc).
	for rank, v := range vecResults {
		scores[v.id] += 1.0 / float64(rrfK+rank+1)
	}

	// Sort by RRF score descending.
	type idScore struct {
		id    string
		score float64
	}
	ranked := make([]idScore, 0, len(scores))
	for id, score := range scores {
		ranked = append(ranked, idScore{id, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	// Collect results, loading any IDs that only came from vec search.
	var results []Memory
	var toLoad []string
	for _, r := range ranked {
		if len(results)+len(toLoad) >= limit {
			break
		}
		if m, ok := memMap[r.id]; ok {
			results = append(results, m)
		} else {
			toLoad = append(toLoad, r.id)
		}
	}

	if len(toLoad) > 0 {
		loaded, err := s.loadMemoriesByIDs(ctx, toLoad)
		if err != nil {
			return results, nil // Return what we have.
		}
		// Insert loaded memories at their ranked positions.
		for _, id := range toLoad {
			if m, ok := loaded[id]; ok {
				results = append(results, m)
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// loadMemoriesByIDs batch-loads memories by their IDs.
func (s *SQLiteStore) loadMemoriesByIDs(ctx context.Context, ids []string) (map[string]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	q := fmt.Sprintf(
		`SELECT id, type, content, metadata, created_at, updated_at FROM memories WHERE id IN (%s)`,
		placeholders,
	)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: load by ids: %w", err)
	}
	defer rows.Close()

	result := make(map[string]Memory, len(ids))
	for rows.Next() {
		var m Memory
		var metaJSON, typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("memory: load scan: %w", err)
		}
		m.Type = MemoryType(typeStr)
		_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
		result[m.ID] = m
	}
	return result, rows.Err()
}

// scanMemories scans rows into a slice of Memory.
func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var results []Memory
	for rows.Next() {
		var m Memory
		var metaJSON, typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("memory: scan: %w", err)
		}
		m.Type = MemoryType(typeStr)
		_ = json.Unmarshal([]byte(metaJSON), &m.Metadata)
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, content, metadata, created_at, updated_at
		 FROM memories
		 WHERE type = ? AND json_extract(metadata, '$.user_id') = ?
		 ORDER BY updated_at DESC
		 LIMIT ?`,
		string(MemoryTypeUser), userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: list by user: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// dupDistanceThreshold is the max vector distance for two memories to be
// considered duplicates. Lower = stricter. Cosine distance typically ranges 0–2.
const dupDistanceThreshold = 0.15

func (s *SQLiteStore) IsDuplicate(ctx context.Context, content string, memType MemoryType) (string, error) {
	if s.embedFn == nil {
		return "", nil
	}
	embedding, err := s.embedFn(ctx, content)
	if err != nil || len(embedding) == 0 {
		return "", nil // can't check, assume not duplicate
	}

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return "", nil
	}

	// KNN search for nearest neighbour, then check type and distance.
	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, v.distance, m.type FROM memories_vec v
		 JOIN memories m ON m.id = v.id
		 WHERE v.embedding MATCH ? AND k = 5`,
		blob,
	)
	if err != nil {
		return "", nil // non-fatal
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var distance float32
		var typ string
		if err := rows.Scan(&id, &distance, &typ); err != nil {
			continue
		}
		if MemoryType(typ) == memType && distance < dupDistanceThreshold {
			return id, nil
		}
	}
	return "", nil
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

// BackfillEmbeddings generates embeddings for memories that don't have them yet.
// Returns the number of memories processed.
func (s *SQLiteStore) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if s.embedFn == nil {
		return 0, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.content FROM memories m
		 WHERE m.id NOT IN (SELECT id FROM memories_vec)
		 LIMIT ?`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("memory: backfill query: %w", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return count, fmt.Errorf("memory: backfill scan: %w", err)
		}

		emb, err := s.embedFn(ctx, content)
		if err != nil || len(emb) == 0 {
			continue
		}

		blob, err := sqlite_vec.SerializeFloat32(emb)
		if err != nil {
			continue
		}

		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
			id, blob,
		); err != nil {
			continue
		}
		count++
	}
	return count, rows.Err()
}

// DB returns the underlying *sql.DB for sharing with other stores (e.g. user.Store).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
