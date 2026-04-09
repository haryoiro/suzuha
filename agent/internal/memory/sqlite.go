//go:build sqlite

package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/external/embedding"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

var sqliteVecInit sync.Once

// memColumns は memories テーブルの SELECT クエリ用の正規カラムリスト。
const memColumns = "id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at"

// memColumnsQualified は指定されたテーブルエイリアス（例: "m"）をプレフィックスに付けた memColumns を返す。
func memColumnsQualified(alias string) string {
	cols := []string{"id", "type", "content", "metadata", "keywords", "topic", "persons", "event_time", "created_at", "updated_at"}
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// scanMem は memColumns の順序に合致する単一のメモリ行をスキャンする。
func scanMem(scanner interface{ Scan(dest ...any) error }) (Memory, error) {
	var m Memory
	var typeStr string
	var metaJSON string
	var keywordsStr, topicStr, personsStr sql.NullString
	var eventTime sql.NullTime

	if err := scanner.Scan(
		&m.ID, &typeStr, &m.Content, &metaJSON,
		&keywordsStr, &topicStr, &personsStr, &eventTime,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return m, err
	}

	m.Type = MemoryType(typeStr)
	if metaJSON != "" {
		if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
			slog.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
		}
	}
	if keywordsStr.Valid {
		json.Unmarshal([]byte(keywordsStr.String), &m.Keywords)
	}
	if topicStr.Valid {
		m.Topic = topicStr.String
	}
	if personsStr.Valid {
		json.Unmarshal([]byte(personsStr.String), &m.Persons)
	}
	if eventTime.Valid {
		t := eventTime.Time
		m.EventTime = &t
	}
	unpackAttachments(&m)
	return m, nil
}

// marshalStringSlice は文字列スライスをJSONエンコードした文字列を返す。空の場合は nil を返す。
func marshalStringSlice(s []string) any {
	if len(s) == 0 {
		return nil
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// nullTimePtr は *time.Time から sql.NullTime を返す。
func nullTimePtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// nullString はSQL挿入用に *string（空の場合は nil）を返す。
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SQLiteStore implements Store using SQLite + sqlite-vec + FTS5.
type SQLiteStore struct {
	db         *sql.DB
	embedder   embedding.Embedder
	mediaStore MediaStore
	onSave     func() // optional hook called on successful Save
	logger     *slog.Logger
	embedSig   chan struct{} // signals the background embedding worker
}

// SetMediaStore sets the media store for loading attachments during embedding.
func (s *SQLiteStore) SetMediaStore(ms MediaStore) { s.mediaStore = ms }

// NewSQLiteStore opens or creates a SQLite database at dbPath.
// If runMigrations is true, pending schema migrations are applied.
// Typically only the agent process should run migrations to avoid race conditions.
func NewSQLiteStore(dbPath string, embedder embedding.Embedder, runMigrations bool, logger *slog.Logger) (*SQLiteStore, error) {
	sqliteVecInit.Do(sqlite_vec.Auto)
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

	return &SQLiteStore{db: db, embedder: embedder, logger: logger, embedSig: make(chan struct{}, 1)}, nil
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
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	// Delete stale FTS entry if it exists (INSERT OR REPLACE on memories may change rowid).
	tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID)
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
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	// Delete stale FTS entry if it exists (INSERT OR REPLACE on memories may change rowid).
	tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID)
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
// Also packs Attachments into Metadata for JSON storage.
func (s *SQLiteStore) initMemFields(mem *Memory) {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := time.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now
	packAttachments(mem)
}

// packAttachments writes mem.Attachments into mem.Metadata["attachments"].
// If Attachments is empty, removes the key.
func packAttachments(mem *Memory) {
	if len(mem.Attachments) == 0 {
		if mem.Metadata != nil {
			delete(mem.Metadata, "attachments")
		}
		return
	}
	if mem.Metadata == nil {
		mem.Metadata = make(map[string]any)
	}
	raw := make([]map[string]string, len(mem.Attachments))
	for i, a := range mem.Attachments {
		raw[i] = map[string]string{
			"key":       a.Key,
			"modality":  a.Modality,
			"mime_type": a.MimeType,
		}
	}
	mem.Metadata["attachments"] = raw
}

// unpackAttachments extracts Attachments from Metadata["attachments"].
func unpackAttachments(mem *Memory) {
	if mem.Metadata == nil {
		return
	}
	raw, ok := mem.Metadata["attachments"]
	if !ok {
		return
	}
	arr, ok := raw.([]any)
	if !ok {
		return
	}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		a := Attachment{}
		if v, ok := m["key"].(string); ok {
			a.Key = v
		}
		if v, ok := m["modality"].(string); ok {
			a.Modality = v
		}
		if v, ok := m["mime_type"].(string); ok {
			a.MimeType = v
		}
		if a.Key != "" {
			mem.Attachments = append(mem.Attachments, a)
		}
	}
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
	const maxBackoff = 10 * time.Minute

	backoff := time.Duration(0)

	for {
		wait := pollInterval
		if backoff > 0 {
			wait = backoff
		}

		select {
		case <-ctx.Done():
			return
		case <-s.embedSig:
		case <-time.After(wait):
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
				// Exponential backoff on error.
				if backoff == 0 {
					backoff = pollInterval
				} else {
					backoff *= 2
				}
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				s.logger.Info("embedding worker: 次回リトライまで待機", "backoff", backoff)
				break
			}
			backoff = 0 // reset on success
			if n == 0 {
				break
			}
			s.logger.Info("embedding worker: バックフィル完了", "count", n)
		}
	}
}

func (s *SQLiteStore) Search(ctx context.Context, query string, limit int) ([]Memory, error) {
	return s.SearchWithContext(ctx, query, limit, SymbolicFilter{})
}

// SearchWithContext は3軸ハイブリッド検索（FTS + Vec + Symbolic）を行う。
func (s *SQLiteStore) SearchWithContext(ctx context.Context, query string, limit int, filter SymbolicFilter) ([]Memory, error) {
	overFetch := limit * 2

	// 軸1: FTSキーワード検索
	ftsResults, ftsErr := s.searchFTS(ctx, query, "", overFetch, time.Time{})

	// 軸2: ベクトル類似度検索
	var vecResults []scoredID
	var vecErr error
	if s.embedder != nil {
		vecResults, vecErr = s.searchVec(ctx, query, "", overFetch)
	}

	// 軸3: Symbolic 検索（構造化フィールドフィルタ）
	symResults, symErr := s.searchSymbolic(ctx, filter, "", overFetch)
	if symErr != nil {
		s.logger.Warn("memory: シンボリック検索に失敗、スキップ", "error", symErr)
	}

	// 全軸が失敗した場合はエラーを返す。
	hasFTS := ftsErr == nil && len(ftsResults) > 0
	hasVec := vecErr == nil && len(vecResults) > 0
	hasSym := symErr == nil && len(symResults) > 0
	if !hasFTS && !hasVec && !hasSym {
		if ftsErr != nil {
			return nil, ftsErr
		}
		return nil, nil
	}

	// 単軸の場合は RRF のオーバーヘッドを避ける。
	axisCount := 0
	if hasFTS {
		axisCount++
	}
	if hasVec {
		axisCount++
	}
	if hasSym {
		axisCount++
	}
	if axisCount == 1 {
		if hasFTS {
			if len(ftsResults) > limit {
				ftsResults = ftsResults[:limit]
			}
			return ftsResults, nil
		}
		var ids []string
		if hasVec {
			for _, v := range vecResults {
				ids = append(ids, v.id)
				if len(ids) >= limit {
					break
				}
			}
		} else {
			for _, sym := range symResults {
				ids = append(ids, sym.id)
				if len(ids) >= limit {
					break
				}
			}
		}
		loaded, err := s.loadMemoriesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		var results []Memory
		for _, id := range ids {
			if m, ok := loaded[id]; ok {
				results = append(results, m)
			}
		}
		return results, nil
	}

	// 複数軸: 3軸 RRF マージ。
	return s.rrfMerge3(ctx, ftsResults, vecResults, symResults, limit)
}

func (s *SQLiteStore) SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]Memory, error) {
	if s.embedder == nil {
		return nil, fmt.Errorf("memory: embedder が設定されていません")
	}
	emb, err := s.embedder.Embed(ctx, parts)
	if err != nil {
		return nil, fmt.Errorf("memory: パーツの埋め込み生成に失敗: %w", err)
	}
	vecResults, err := s.searchVecByEmbedding(ctx, emb, "", limit)
	if err != nil {
		return nil, err
	}
	if len(vecResults) == 0 {
		return nil, nil
	}
	// Load full memories.
	ids := make([]string, len(vecResults))
	for i, r := range vecResults {
		ids[i] = r.id
	}
	loaded, err := s.loadMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	var results []Memory
	for _, id := range ids {
		if m, ok := loaded[id]; ok {
			results = append(results, m)
		}
	}
	return results, nil
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

	// 2. Vector similarity search (if embedder is available).
	var vecResults []scoredID
	var vecErr error
	if s.embedder != nil {
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

// escapeFTS5Query escapes a raw string for use in an FTS5 MATCH clause.
// Wraps the query in double quotes so FTS5 treats it as a literal phrase,
// escaping any embedded double quotes.
func escapeFTS5Query(query string) string {
	escaped := strings.ReplaceAll(query, `"`, `""`)
	return `"` + escaped + `"`
}

// searchFTS performs keyword search via FTS5 (trigram) or LIKE fallback.
func (s *SQLiteStore) searchFTS(ctx context.Context, query string, memType MemoryType, limit int, since time.Time) ([]Memory, error) {
	var q string
	var args []any

	mc := memColumnsQualified("m")
	if len([]rune(query)) >= 3 {
		q = fmt.Sprintf(`SELECT %s
		     FROM memories m
		     JOIN memories_fts f ON f.rowid = m.rowid
		     WHERE memories_fts MATCH ?`, mc)
		args = []any{escapeFTS5Query(query)}
	} else {
		q = fmt.Sprintf(`SELECT %s
		     FROM memories m
		     WHERE m.content LIKE ?`, mc)
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

// Modality boost factors for vec search distance scaling.
// Lower distance = higher similarity, so we multiply distance by 1/boost
// to rank multimodal memories higher.
const (
	boostImage = 1.5
	boostAudio = 1.4
)

// searchVec performs KNN vector search via sqlite-vec using a text query.
func (s *SQLiteStore) searchVec(ctx context.Context, query string, memType MemoryType, limit int) ([]scoredID, error) {
	emb, err := s.embedder.Embed(ctx, []embedding.Part{embedding.TextPart(query)})
	if err != nil {
		return nil, fmt.Errorf("memory: クエリの埋め込み生成に失敗: %w", err)
	}
	return s.searchVecByEmbedding(ctx, emb, memType, limit)
}

// searchVecByEmbedding performs KNN vector search with a pre-computed embedding.
func (s *SQLiteStore) searchVecByEmbedding(ctx context.Context, emb []float32, memType MemoryType, limit int) ([]scoredID, error) {
	if len(emb) == 0 {
		return nil, nil
	}

	blob, err := sqlite_vec.SerializeFloat32(emb)
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

	// Apply modality boost: reduce distance for memories with attachments
	// so they rank higher in RRF merge.
	if len(results) > 0 {
		results = s.applyModalityBoost(ctx, results)
	}

	return results, nil
}

// applyModalityBoost adjusts vec distances for multimodal memories.
// Memories with image/audio attachments get their distance divided by a boost factor,
// effectively ranking them higher in search results.
func (s *SQLiteStore) applyModalityBoost(ctx context.Context, results []scoredID) []scoredID {
	if len(results) == 0 {
		return results
	}

	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.id
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	// Find which IDs have attachments in their metadata.
	q := fmt.Sprintf(
		`SELECT id, json_extract(metadata, '$.attachments') FROM memories WHERE id IN (%s)`,
		placeholders)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return results // non-fatal
	}
	defer rows.Close()

	boosts := make(map[string]float64)
	for rows.Next() {
		var id string
		var attJSON sql.NullString
		if err := rows.Scan(&id, &attJSON); err != nil {
			continue
		}
		if !attJSON.Valid || attJSON.String == "" || attJSON.String == "null" {
			continue
		}
		// Determine boost from attachment modalities.
		var atts []Attachment
		if err := json.Unmarshal([]byte(attJSON.String), &atts); err != nil {
			continue
		}
		for _, a := range atts {
			switch a.Modality {
			case "image":
				if boosts[id] < boostImage {
					boosts[id] = boostImage
				}
			case "audio":
				if boosts[id] < boostAudio {
					boosts[id] = boostAudio
				}
			}
		}
	}

	if len(boosts) == 0 {
		return results
	}

	// Apply boost: divide distance by boost factor.
	for i, r := range results {
		if b, ok := boosts[r.id]; ok && b > 0 {
			results[i].distance /= float32(b)
		}
	}

	// Re-sort by boosted distance.
	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	return results
}

// filterVecByType filters vec results by memory type using a DB lookup.
func (s *SQLiteStore) filterVecByType(ctx context.Context, results []scoredID, memType MemoryType, limit int) ([]scoredID, error) {
	return s.filterVecResults(ctx, results, "type = ?", string(memType), limit)
}

// filterVecBySince filters vec results by creation time.
func (s *SQLiteStore) filterVecBySince(ctx context.Context, results []scoredID, since time.Time, limit int) ([]scoredID, error) {
	return s.filterVecResults(ctx, results, "created_at >= ?", since, limit)
}

// filterVecResults filters vec results by an arbitrary WHERE clause on the memories table.
func (s *SQLiteStore) filterVecResults(ctx context.Context, results []scoredID, whereClause string, whereArg any, limit int) ([]scoredID, error) {
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
	args = append(args, whereArg)

	q := fmt.Sprintf(`SELECT id FROM memories WHERE id IN (%s) AND %s`, placeholders, whereClause)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: フィルタに失敗 (%s): %w", whereClause, err)
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

// searchSymbolic は構造化フィールド（persons, topic, event_time）でフィルタリング検索を行う。
// フィルタが空の場合は nil を返す。結果は updated_at DESC で順序付けされる。
func (s *SQLiteStore) searchSymbolic(ctx context.Context, filter SymbolicFilter, memType MemoryType, limit int) ([]scoredID, error) {
	if filter.IsEmpty() {
		return nil, nil
	}

	var clauses []string
	var args []any

	// Persons フィルタ: persons JSON 配列に指定 ID のいずれかが含まれるメモリにマッチ。
	if len(filter.PersonIDs) > 0 {
		placeholders := strings.Repeat("?,", len(filter.PersonIDs))
		placeholders = placeholders[:len(placeholders)-1]
		clauses = append(clauses, fmt.Sprintf(
			`m.id IN (SELECT m2.id FROM memories m2, json_each(m2.persons) AS j WHERE j.value IN (%s))`,
			placeholders))
		for _, pid := range filter.PersonIDs {
			args = append(args, pid)
		}
	}

	// Topic プレフィックスフィルタ。
	if filter.TopicPrefix != "" {
		clauses = append(clauses, `m.topic LIKE ?`)
		args = append(args, filter.TopicPrefix+"%")
	}

	// 時間フィルタ（event_time 優先、NULL なら created_at で代替）。
	if !filter.Since.IsZero() {
		clauses = append(clauses, `COALESCE(m.event_time, m.created_at) >= ?`)
		args = append(args, filter.Since)
	}

	if memType != "" {
		clauses = append(clauses, `m.type = ?`)
		args = append(args, string(memType))
	}

	if len(clauses) == 0 {
		return nil, nil
	}

	where := strings.Join(clauses, " AND ")
	q := fmt.Sprintf(`SELECT m.id FROM memories m WHERE %s ORDER BY m.updated_at DESC LIMIT ?`, where)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: シンボリック検索に失敗: %w", err)
	}
	defer rows.Close()

	var results []scoredID
	for rows.Next() {
		var r scoredID
		if err := rows.Scan(&r.id); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// rrfMerge3 は FTS, Vec, Symbolic の3軸検索結果を Reciprocal Rank Fusion で統合する。
func (s *SQLiteStore) rrfMerge3(ctx context.Context, ftsResults []Memory, vecResults []scoredID, symResults []scoredID, limit int) ([]Memory, error) {
	scores := make(map[string]float64)
	memMap := make(map[string]Memory)

	// FTS はランクベースのスコアを付与。
	for rank, m := range ftsResults {
		scores[m.ID] += 1.0 / float64(rrfK+rank+1)
		memMap[m.ID] = m
	}

	// Vec はランクベースのスコアを付与（距離昇順で既にソート済み）。
	for rank, v := range vecResults {
		scores[v.id] += 1.0 / float64(rrfK+rank+1)
	}

	// Symbolic はランクベースのスコアを付与（updated_at DESC でソート済み）。
	for rank, sym := range symResults {
		scores[sym.id] += 1.0 / float64(rrfK+rank+1)
	}

	// RRF スコア降順でソート。
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

	// 結果を収集。memMap にないIDはバッチロードする。
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
			return results, nil
		}
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
		`SELECT %s FROM memories WHERE id IN (%s)`,
		memColumns, placeholders,
	)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: IDによる一括読み込みに失敗: %w", err)
	}
	defer rows.Close()

	result := make(map[string]Memory, len(ids))
	for rows.Next() {
		m, err := scanMem(rows)
		if err != nil {
			return nil, fmt.Errorf("memory: 読み込み時のスキャンに失敗: %w", err)
		}
		result[m.ID] = m
	}
	return result, rows.Err()
}

// scanMemories scans rows into a slice of Memory.
// The query must select memColumns in order.
func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var results []Memory
	for rows.Next() {
		m, err := scanMem(rows)
		if err != nil {
			return nil, fmt.Errorf("memory: スキャンに失敗: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT %s FROM memories m
		 WHERE m.type = ? AND m.id IN (
		   SELECT m2.id FROM memories m2, json_each(m2.persons) AS j WHERE j.value = ?
		 )
		 ORDER BY m.updated_at DESC
		 LIMIT ?`, memColumnsQualified("m")),
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
		fmt.Sprintf(`SELECT %s
		 FROM memories m
		 WHERE m.type = ? AND m.id IN (
		   SELECT m2.id FROM memories m2, json_each(m2.persons) AS j WHERE j.value = ?
		 )
		 ORDER BY m.updated_at DESC
		 LIMIT ?`, memColumnsQualified("m")),
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

func (s *SQLiteStore) IsDuplicate(ctx context.Context, content string, memType MemoryType) (string, []float32, error) {
	// Phase 1: cheap FTS pre-check — skip embedding API call for exact text matches.
	if ftsID := s.ftsExactMatch(ctx, content, memType); ftsID != "" {
		return ftsID, nil, nil
	}

	if s.embedder == nil {
		return "", nil, nil
	}
	emb, err := s.embedder.Embed(ctx, []embedding.Part{embedding.TextPart(content)})
	if err != nil || len(emb) == 0 {
		return "", nil, nil // can't check, assume not duplicate
	}

	dupID := s.knnDupCheck(ctx, emb, memType)
	return dupID, emb, nil
}

// IsDuplicateBatch checks multiple candidates in a single batch to minimise
// embedding API calls. FTS pre-check is applied first, and remaining
// candidates are embedded in one EmbedBatch call.
func (s *SQLiteStore) IsDuplicateBatch(ctx context.Context, candidates []DupCandidate) ([]DupResult, error) {
	results := make([]DupResult, len(candidates))

	// Phase 1: FTS pre-check.
	var needEmbed []int
	for i, c := range candidates {
		if ftsID := s.ftsExactMatch(ctx, c.Content, c.Type); ftsID != "" {
			results[i].DupID = ftsID
		} else {
			needEmbed = append(needEmbed, i)
		}
	}

	if len(needEmbed) == 0 || s.embedder == nil {
		return results, nil
	}

	// Phase 2: batch embed.
	inputs := make([][]embedding.Part, len(needEmbed))
	for j, idx := range needEmbed {
		inputs[j] = []embedding.Part{embedding.TextPart(candidates[idx].Content)}
	}

	vectors, err := s.embedder.EmbedBatch(ctx, inputs)
	if err != nil {
		// Non-fatal: return FTS results, rest treated as non-duplicate w/o embedding.
		s.logger.Warn("memory: バッチ埋め込みに失敗 (dedup)", "error", err)
		return results, nil
	}

	// Phase 3: KNN check per embedding.
	for j, idx := range needEmbed {
		emb := vectors[j]
		results[idx].Embedding = emb
		if len(emb) == 0 {
			continue
		}
		if dupID := s.knnDupCheck(ctx, emb, candidates[idx].Type); dupID != "" {
			results[idx].DupID = dupID
		}
	}

	return results, nil
}

// ftsExactMatch does a cheap FTS5 phrase search to find an existing memory
// with identical text. Returns the matching ID or empty string.
func (s *SQLiteStore) ftsExactMatch(ctx context.Context, content string, memType MemoryType) string {
	if len([]rune(content)) < 3 {
		return ""
	}
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT m.id FROM memories m
		 JOIN memories_fts f ON f.rowid = m.rowid
		 WHERE memories_fts MATCH ? AND m.type = ?
		 LIMIT 1`,
		escapeFTS5Query(content), string(memType),
	).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// knnDupCheck performs KNN vector search and returns the ID of a same-type
// memory within dupDistanceThreshold, or empty string.
func (s *SQLiteStore) knnDupCheck(ctx context.Context, emb []float32, memType MemoryType) string {
	blob, err := sqlite_vec.SerializeFloat32(emb)
	if err != nil {
		return ""
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, v.distance, m.type FROM memories_vec v
		 JOIN memories m ON m.id = v.id
		 WHERE v.embedding MATCH ? AND k = 5`,
		blob,
	)
	if err != nil {
		return ""
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
			return id
		}
	}
	return ""
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
		args = append(args, escapeFTS5Query(opts.Query))
	}

	// Count total.
	var total int
	countQ := fmt.Sprintf("SELECT count(*) FROM memories m WHERE %s", where)
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("memory: 一覧の件数取得に失敗: %w", err)
	}

	// Fetch page.
	q := fmt.Sprintf(
		"SELECT %s FROM memories m WHERE %s ORDER BY m.%s %s LIMIT ? OFFSET ?",
		memColumnsQualified("m"), where, opts.OrderBy, opts.OrderDir,
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
		m, err := scanMem(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("memory: 一覧のスキャンに失敗: %w", err)
		}
		results = append(results, m)
	}
	return results, total, rows.Err()
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT %s FROM memories WHERE id = ?`, memColumns), id,
	)
	m, err := scanMem(row)
	if err != nil {
		return nil, fmt.Errorf("memory: 取得に失敗: %w", err)
	}
	return &m, nil
}

func (s *SQLiteStore) Update(ctx context.Context, mem *Memory) error {
	mem.UpdatedAt = time.Now()
	packAttachments(mem)

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
		`UPDATE memories SET type = ?, content = ?, metadata = ?, keywords = ?, topic = ?, persons = ?, event_time = ?, updated_at = ? WHERE id = ?`,
		string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.UpdatedAt, mem.ID,
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
		fmt.Sprintf(`SELECT %s FROM memories
		 WHERE type = ?
		 ORDER BY updated_at DESC
		 LIMIT ?`, memColumns),
		string(memType), limit)
	if err != nil {
		return nil, fmt.Errorf("memory: タイプ別一覧の取得に失敗: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

func (s *SQLiteStore) ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT %s FROM memories
		 WHERE type = ? AND created_at > ?
		 ORDER BY created_at DESC
		 LIMIT ?`, memColumns),
		string(memType), since, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: タイプ別最新一覧の取得に失敗: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) ListRecent(ctx context.Context, since time.Time, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT %s FROM memories
		 WHERE created_at > ?
		 ORDER BY created_at DESC
		 LIMIT ?`, memColumns),
		since, limit)
	if err != nil {
		return nil, fmt.Errorf("memory: 最新一覧の取得に失敗: %w", err)
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

func (s *SQLiteStore) ListEmbeddedMemories(ctx context.Context) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT %s
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.created_at`, memColumnsQualified("m")))
	if err != nil {
		return nil, fmt.Errorf("memory: 埋め込み済みメモリの取得に失敗: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *SQLiteStore) ListAllEmbeddings(ctx context.Context) (map[string][]float32, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, embedding FROM memories_vec`)
	if err != nil {
		return nil, fmt.Errorf("memory: 埋め込みの取得に失敗: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]float32)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec, err := deserializeFloat32Vec(blob)
		if err != nil {
			continue
		}
		result[id] = vec
	}
	return result, rows.Err()
}

// deserializeFloat32Vec はリトルエンディアンの float32 バイト列を []float32 に変換する。
func deserializeFloat32Vec(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("不正なblobの長さ: %d", len(blob))
	}
	n := len(blob) / 4
	vec := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(blob[i*4 : (i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec, nil
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
		fmt.Sprintf(`SELECT %s
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.updated_at DESC`, memColumnsQualified("m")))
	if err != nil {
		return nil, fmt.Errorf("memory: 重複検出用の一覧取得に失敗: %w", err)
	}
	defer rows.Close()

	var all []memEntry
	for rows.Next() {
		m, err := scanMem(rows)
		if err != nil {
			continue
		}
		all = append(all, memEntry{Memory: m})
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
// Uses EmbedBatch for efficient bulk processing. Returns the number of memories processed.
func (s *SQLiteStore) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if s.embedder == nil {
		return 0, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.content, m.metadata FROM memories m
		 WHERE m.id NOT IN (SELECT id FROM memories_vec)
		 LIMIT ?`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("memory: バックフィルクエリに失敗: %w", err)
	}
	defer rows.Close()

	type entry struct {
		id      string
		content string
		atts    []Attachment
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var metaJSON sql.NullString
		if err := rows.Scan(&e.id, &e.content, &metaJSON); err != nil {
			return 0, fmt.Errorf("memory: バックフィルのスキャンに失敗: %w", err)
		}
		// Extract attachments from metadata.
		if metaJSON.Valid && metaJSON.String != "" {
			var m Memory
			m.Metadata = make(map[string]any)
			if err := json.Unmarshal([]byte(metaJSON.String), &m.Metadata); err == nil {
				unpackAttachments(&m)
				e.atts = m.Attachments
			}
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	// Build batch inputs, including image attachments from MediaStore.
	// Filter out entries with empty content to avoid API errors.
	type indexedInput struct {
		idx   int
		parts []embedding.Part
	}
	var valid []indexedInput
	for i, e := range entries {
		if strings.TrimSpace(e.content) == "" {
			s.logger.Warn("embedding backfill: 空コンテンツをスキップ", "id", e.id)
			continue
		}
		parts := []embedding.Part{embedding.TextPart(e.content)}
		if s.mediaStore != nil {
			for _, att := range e.atts {
				if att.Modality == "image" {
					data, err := s.mediaStore.Get(ctx, att.Key)
					if err != nil {
						continue
					}
					parts = append(parts, embedding.ImagePart(data, att.MimeType))
				}
			}
		}
		valid = append(valid, indexedInput{idx: i, parts: parts})
	}
	if len(valid) == 0 {
		return 0, nil
	}

	inputs := make([][]embedding.Part, len(valid))
	for i, v := range valid {
		inputs[i] = v.parts
	}

	vectors, err := s.embedder.EmbedBatch(ctx, inputs)
	if err != nil {
		// Log which entries were in the batch for debugging.
		for _, v := range valid {
			s.logger.Warn("embedding backfill: バッチ内エントリ",
				"id", entries[v.idx].id,
				"content_len", len(entries[v.idx].content),
				"attachments", len(entries[v.idx].atts))
		}
		return 0, fmt.Errorf("memory: バッチ埋め込みに失敗: %w", err)
	}

	var count int
	for i, vec := range vectors {
		if len(vec) == 0 {
			continue
		}
		blob, err := sqlite_vec.SerializeFloat32(vec)
		if err != nil {
			continue
		}
		entryID := entries[valid[i].idx].id
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
			entryID, blob,
		); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

// DB returns the underlying *sql.DB for sharing with other stores (e.g. user.Store).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
