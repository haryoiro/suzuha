//go:build sqlite

package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/haryoiro/suzuha/external/embedding"
)

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
