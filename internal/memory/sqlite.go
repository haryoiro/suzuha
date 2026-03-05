package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

func init() {
	sqlite_vec.Auto()
}

// SQLiteStore implements Store using SQLite + sqlite-vec + FTS5.
type SQLiteStore struct {
	db       *sql.DB
	embedFn  EmbedFunc
	onSave   func() // optional hook called on successful Save
	logger   *slog.Logger
	embedSig chan struct{} // signals the background embedding worker
}

// NewSQLiteStore opens or creates a SQLite database at dbPath.
// If runMigrations is true, pending schema migrations are applied.
// Typically only the agent process should run migrations to avoid race conditions.
func NewSQLiteStore(dbPath string, embedFn EmbedFunc, runMigrations bool, logger *slog.Logger) (*SQLiteStore, error) {
	if logger == nil {
		logger = slog.Default()
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("memory: DB接続に失敗: %w", err)
	}

	// Enable WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: WALモードの設定に失敗: %w", err)
	}

	if runMigrations {
		if err := migrate(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: マイグレーションに失敗: %w", err)
		}
	}

	return &SQLiteStore{db: db, embedFn: embedFn, logger: logger, embedSig: make(chan struct{}, 1)}, nil
}

// SetOnSave registers a callback invoked after each successful Save.
func (s *SQLiteStore) SetOnSave(fn func()) { s.onSave = fn }

func (s *SQLiteStore) Save(ctx context.Context, mem *Memory) error {
	// If embedding is already provided, save everything inline.
	if len(mem.Embedding) > 0 {
		return s.saveWithEmbedding(ctx, mem)
	}
	// Otherwise, save content + FTS immediately; embedding is generated
	// asynchronously by the background worker (RunEmbeddingWorker).
	if err := s.saveContentAndFTS(ctx, mem); err != nil {
		return err
	}
	s.notifyEmbedWorker()
	return nil
}

// saveWithEmbedding persists content, FTS index, and vector embedding in one transaction.
func (s *SQLiteStore) saveWithEmbedding(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	); err != nil {
		return fmt.Errorf("memory: FTSインデックスの挿入に失敗: %w", err)
	}

	if blob, err := sqlite_vec.SerializeFloat32(mem.Embedding); err == nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
			mem.ID, blob,
		); err != nil {
			s.logger.Warn("memory: ベクトルの挿入に失敗", "id", mem.ID, "error", err)
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

// saveContentAndFTS persists content and FTS index without generating an embedding.
// The embedding will be backfilled asynchronously by RunEmbeddingWorker.
func (s *SQLiteStore) saveContentAndFTS(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	); err != nil {
		return fmt.Errorf("memory: FTSインデックスの挿入に失敗: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if s.onSave != nil {
		s.onSave()
	}
	return nil
}

// initMemFields sets ID and timestamps if not already set.
func (s *SQLiteStore) initMemFields(mem *Memory) {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := time.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now
}

// notifyEmbedWorker sends a non-blocking signal to wake up the embedding worker.
func (s *SQLiteStore) notifyEmbedWorker() {
	select {
	case s.embedSig <- struct{}{}:
	default: // already signaled
	}
}

// RunEmbeddingWorker processes pending embeddings in the background.
// It wakes up on signal (after Save) or periodically. Call as a goroutine.
func (s *SQLiteStore) RunEmbeddingWorker(ctx context.Context) {
	const batchSize = 20
	const pollInterval = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.embedSig:
		case <-time.After(pollInterval):
		}

		// Drain any extra signals.
		for {
			select {
			case <-s.embedSig:
			default:
				goto process
			}
		}
	process:
		for {
			n, err := s.BackfillEmbeddings(ctx, batchSize)
			if err != nil {
				s.logger.Warn("embedding worker: バックフィルでエラー発生", "error", err)
				break
			}
			if n == 0 {
				break
			}
			s.logger.Info("embedding worker: バックフィル完了", "count", n)
		}
	}
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
		filtered, filterErr := s.filterVecBySince(ctx, vecResults, since, limit*2)
		if filterErr != nil {
			s.logger.Warn("memory: ベクトルの時間フィルタに失敗、フィルタなしで続行", "error", filterErr)
		} else {
			vecResults = filtered
		}
	}

	// 3. If both failed, return FTS error.
	if ftsErr != nil && (vecErr != nil || len(vecResults) == 0) {
		return nil, fmt.Errorf("memory: 検索に失敗: %w", ftsErr)
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
		return nil, fmt.Errorf("memory: FTS検索に失敗: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// searchVec performs KNN vector search via sqlite-vec.
func (s *SQLiteStore) searchVec(ctx context.Context, query string, memType MemoryType, limit int) ([]scoredID, error) {
	embedding, err := s.embedFn(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("memory: クエリの埋め込み生成に失敗: %w", err)
	}
	if len(embedding) == 0 {
		return nil, nil
	}

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("memory: 埋め込みのシリアライズに失敗: %w", err)
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
		return nil, fmt.Errorf("memory: ベクトル検索に失敗: %w", err)
	}
	defer rows.Close()

	var results []scoredID
	for rows.Next() {
		var r scoredID
		if err := rows.Scan(&r.id, &r.distance); err != nil {
			return nil, fmt.Errorf("memory: ベクトル結果のスキャンに失敗: %w", err)
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
		return nil, fmt.Errorf("memory: タイプによるフィルタに失敗: %w", err)
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
		return nil, fmt.Errorf("memory: 時間によるフィルタに失敗: %w", err)
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
		return nil, fmt.Errorf("memory: IDによる一括読み込みに失敗: %w", err)
	}
	defer rows.Close()

	result := make(map[string]Memory, len(ids))
	for rows.Next() {
		var m Memory
		var metaJSON, typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("memory: 読み込み時のスキャンに失敗: %w", err)
		}
		m.Type = MemoryType(typeStr)
		if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
			slog.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
		}
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
			return nil, fmt.Errorf("memory: スキャンに失敗: %w", err)
		}
		m.Type = MemoryType(typeStr)
		if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
			slog.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
		}
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
		return nil, fmt.Errorf("memory: ユーザー別一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

func (s *SQLiteStore) ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		 FROM memories m, json_each(json_extract(m.metadata, '$.participants')) AS j
		 WHERE m.type = ? AND j.value = ?
		 ORDER BY m.updated_at DESC
		 LIMIT ?`,
		string(MemoryTypeEpisode), userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: 参加者別エピソード一覧の取得に失敗: %w", err)
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

	// KNN search for nearest neighbor, then check type and distance.
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
		return nil, 0, fmt.Errorf("memory: 一覧の件数取得に失敗: %w", err)
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
		return nil, 0, fmt.Errorf("memory: 一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		var m Memory
		var metaJSON string
		var typeStr string
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &metaJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("memory: 一覧のスキャンに失敗: %w", err)
		}
		m.Type = MemoryType(typeStr)
		if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
			s.logger.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
		}
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
		return nil, fmt.Errorf("memory: 取得に失敗: %w", err)
	}
	m.Type = MemoryType(typeStr)
	if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
		s.logger.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
	}
	return &m, nil
}

func (s *SQLiteStore) Update(ctx context.Context, mem *Memory) error {
	mem.UpdatedAt = time.Now()

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE memories SET type = ?, content = ?, metadata = ?, updated_at = ? WHERE id = ?`,
		string(mem.Type), mem.Content, string(metadataJSON), mem.UpdatedAt, mem.ID,
	)
	if err != nil {
		return fmt.Errorf("memory: 更新に失敗: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		s.logger.Warn("memory: 影響行数の取得に失敗", "id", mem.ID, "error", raErr)
	}
	if n == 0 {
		return fmt.Errorf("memory: 見つかりません: %s", mem.ID)
	}

	// Rebuild FTS index for this row.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID); err != nil {
		s.logger.Warn("memory: 更新時のFTS削除に失敗", "id", mem.ID, "error", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content); err != nil {
		s.logger.Warn("memory: 更新時のFTS挿入に失敗", "id", mem.ID, "error", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	// Delete from FTS.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, id); err != nil {
		s.logger.Warn("memory: FTSの削除に失敗", "id", id, "error", err)
	}
	// Delete from vec.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_vec WHERE id = ?`, id); err != nil {
		s.logger.Warn("memory: ベクトルの削除に失敗", "id", id, "error", err)
	}
	// Delete from main table.
	res, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("memory: 削除に失敗: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		s.logger.Warn("memory: 削除時の影響行数の取得に失敗", "id", id, "error", raErr)
	}
	if n == 0 {
		return fmt.Errorf("memory: 見つかりません: %s", id)
	}

	return tx.Commit()
}

func (s *SQLiteStore) ListByType(ctx context.Context, memType MemoryType, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, content, metadata, created_at, updated_at
		 FROM memories
		 WHERE type = ?
		 ORDER BY updated_at DESC
		 LIMIT ?`,
		string(memType), limit)
	if err != nil {
		return nil, fmt.Errorf("memory: タイプ別一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

func (s *SQLiteStore) ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, content, metadata, created_at, updated_at
		 FROM memories
		 WHERE type = ? AND created_at > ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		string(memType), since, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: タイプ別最新一覧の取得に失敗: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) VecStats(ctx context.Context) (total, embedded int, err error) {
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories").Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("memory: ベクトル統計の全件数取得に失敗: %w", err)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories_vec").Scan(&embedded); err != nil {
		return 0, 0, fmt.Errorf("memory: ベクトル統計の埋め込み済み件数取得に失敗: %w", err)
	}
	return total, embedded, nil
}

func (s *SQLiteStore) FindDuplicates(ctx context.Context, k int, threshold float64) ([]DuplicateGroup, error) {
	if k <= 0 {
		k = 10
	}
	// Load all memories with embeddings.
	type memEntry struct {
		Memory
		visited bool
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("memory: 重複検出用の一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var all []memEntry
	for rows.Next() {
		var e memEntry
		var metaJSON string
		if err := rows.Scan(&e.ID, &e.Type, &e.Content, &metaJSON, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		if metaJSON != "" {
			json.Unmarshal([]byte(metaJSON), &e.Metadata)
		}
		all = append(all, e)
	}

	var groups []DuplicateGroup
	for i := range all {
		if all[i].visited {
			continue
		}
		neighRows, err := s.db.QueryContext(ctx,
			`SELECT v2.id, v2.distance
			 FROM memories_vec v1
			 JOIN memories_vec v2 ON v2.embedding MATCH v1.embedding AND v2.k = ?
			 WHERE v1.id = ?`, k, all[i].ID)
		if err != nil {
			continue
		}

		group := []Memory{all[i].Memory}
		all[i].visited = true

		for neighRows.Next() {
			var nid string
			var dist float32
			if err := neighRows.Scan(&nid, &dist); err != nil {
				continue
			}
			if nid == all[i].ID || float64(dist) >= threshold {
				continue
			}
			for j := range all {
				if all[j].ID == nid && !all[j].visited && all[j].Type == all[i].Type {
					group = append(group, all[j].Memory)
					all[j].visited = true
					break
				}
			}
		}
		neighRows.Close()

		if len(group) > 1 {
			groups = append(groups, DuplicateGroup{Memories: group})
		}
	}
	return groups, nil
}

func (s *SQLiteStore) DeleteBatch(ctx context.Context, ids []string) (int, error) {
	var deleted int
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			s.logger.Warn("memory: 一括削除でスキップ", "id", id, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *SQLiteStore) SaveRaw(ctx context.Context, mem *Memory) error {
	// SaveRaw saves without embedding and without signaling the worker.
	// Used by admin merge endpoint where BackfillEmbeddings handles it later.
	return s.saveContentAndFTS(ctx, mem)
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
		return 0, fmt.Errorf("memory: バックフィルクエリに失敗: %w", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return count, fmt.Errorf("memory: バックフィルのスキャンに失敗: %w", err)
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
