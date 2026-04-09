//go:build sqlite

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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
