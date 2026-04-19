package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/port/embedder"
	"github.com/pgvector/pgvector-go"
)

// Search は BM25 (pg_search) でテキスト検索する。
// embedder がある場合はベクトル検索も行い RRF でマージする。
func (s *DBStore) Search(ctx context.Context, query string, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, time.Time{})
}

func (s *DBStore) SearchWithContext(ctx context.Context, query string, limit int, filter SymbolicFilter) ([]Memory, error) {
	// TODO(haryoiro): Symbolic filter 対応
	return s.searchInternal(ctx, query, "", limit, time.Time{})
}

func (s *DBStore) SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, memType, limit, time.Time{})
}

func (s *DBStore) SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, since)
}

func (s *DBStore) searchInternal(ctx context.Context, query string, memType MemoryType, limit int, since time.Time) ([]Memory, error) {
	// BM25 検索
	var wheres []string
	var args []any
	argN := 1

	wheres = append(wheres, fmt.Sprintf("content ||| $%d", argN))
	args = append(args, query)
	argN++

	if memType != "" {
		wheres = append(wheres, fmt.Sprintf("type = $%d", argN))
		args = append(args, string(memType))
		argN++
	}
	if !since.IsZero() {
		wheres = append(wheres, fmt.Sprintf("created_at >= $%d", argN))
		args = append(args, since)
		argN++
	}

	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE %s ORDER BY pdb.score(id) DESC LIMIT $%d`,
		pgMemColumns, strings.Join(wheres, " AND "), argN,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: BM25 search に失敗: %w", err)
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		m, err := scanPGMem(rows)
		if err != nil {
			return nil, fmt.Errorf("memory: scan に失敗: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *DBStore) SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]Memory, error) {
	if s.embedder == nil {
		return nil, nil
	}
	emb, err := s.embedder.Embed(ctx, parts)
	if err != nil {
		return nil, fmt.Errorf("memory: embedding に失敗: %w", err)
	}
	return s.searchVec(ctx, emb, "", limit)
}

func (s *DBStore) searchVec(ctx context.Context, emb []float32, memType MemoryType, limit int) ([]Memory, error) {
	var wheres []string
	var args []any
	argN := 1

	args = append(args, pgvector.NewVector(emb))
	argN++

	if memType != "" {
		wheres = append(wheres, fmt.Sprintf("type = $%d", argN))
		args = append(args, string(memType))
		argN++
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	q := fmt.Sprintf(
		`SELECT %s FROM memories %s ORDER BY embedding <=> $1 LIMIT $%d`,
		pgMemColumns, whereClause, argN,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: vec search に失敗: %w", err)
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		m, err := scanPGMem(rows)
		if err != nil {
			return nil, fmt.Errorf("memory: scan に失敗: %w", err)
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *DBStore) IsDuplicate(ctx context.Context, content string, memType MemoryType) (string, []float32, error) {
	// BM25 exact match check
	var dupID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE content ||| $1 AND type = $2 LIMIT 1`,
		content, string(memType),
	).Scan(&dupID)
	if err == nil {
		return dupID, nil, nil
	}

	if s.embedder == nil {
		return "", nil, nil
	}

	emb, err := s.embedder.Embed(ctx, []embedding.Part{embedding.TextPart(content)})
	if err != nil || len(emb) == 0 {
		return "", nil, nil
	}

	// KNN duplicate check
	var matchID string
	var dist float64
	err = s.db.QueryRowContext(ctx,
		`SELECT id, embedding <=> $1 AS dist FROM memories
		 WHERE type = $2 AND embedding IS NOT NULL
		 ORDER BY embedding <=> $1 LIMIT 1`,
		pgvector.NewVector(emb), string(memType),
	).Scan(&matchID, &dist)
	if err == nil && dist < 0.15 {
		return matchID, emb, nil
	}

	return "", emb, nil
}

func (s *DBStore) IsDuplicateBatch(ctx context.Context, candidates []DupCandidate) ([]DupResult, error) {
	results := make([]DupResult, len(candidates))
	for i, c := range candidates {
		dupID, emb, err := s.IsDuplicate(ctx, c.Content, c.Type)
		if err != nil {
			return nil, err
		}
		results[i] = DupResult{DupID: dupID, Embedding: emb}
	}
	return results, nil
}
