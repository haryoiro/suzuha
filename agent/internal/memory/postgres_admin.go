package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/pgvector/pgvector-go"
)

// Get は ID で単一のメモリを取得する。
func (s *PostgresStore) Get(ctx context.Context, id string) (*Memory, error) {
	q := fmt.Sprintf(`SELECT %s FROM memories WHERE id = $1`, pgMemColumns)
	m, err := scanPGMem(s.db.QueryRowContext(ctx, q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// List はフィルタ・ページネーション付きでメモリ一覧を返す。
func (s *PostgresStore) List(ctx context.Context, opts ListOpts) ([]Memory, int, error) {
	var wheres []string
	var args []any
	argN := 1

	if opts.Type != "" {
		wheres = append(wheres, fmt.Sprintf("type = $%d", argN))
		args = append(args, string(opts.Type))
		argN++
	}
	if opts.Query != "" {
		wheres = append(wheres, fmt.Sprintf("content ||| $%d", argN))
		args = append(args, opts.Query)
		argN++
	}

	whereClause := ""
	if len(wheres) > 0 {
		whereClause = "WHERE " + strings.Join(wheres, " AND ")
	}

	// Count
	var total int
	countQ := "SELECT COUNT(*) FROM memories " + whereClause
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	orderBy := "updated_at"
	if opts.OrderBy == "created_at" {
		orderBy = "created_at"
	}
	orderDir := "DESC"
	if strings.EqualFold(opts.OrderDir, "asc") {
		orderDir = "ASC"
	}

	q := fmt.Sprintf(`SELECT %s FROM memories %s ORDER BY %s %s LIMIT $%d OFFSET $%d`,
		pgMemColumns, whereClause, orderBy, orderDir, argN, argN+1)
	args = append(args, opts.Limit, opts.Offset)

	mems, err := s.queryMems(ctx, q, args...)
	return mems, total, err
}

// Update は既存のメモリを更新する。
func (s *PostgresStore) Update(ctx context.Context, mem *Memory) error {
	mem.UpdatedAt = jtime.Now()

	meta := mem.Metadata
	if len(mem.Attachments) > 0 {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["attachments"] = mem.Attachments
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET type = $1, content = $2, metadata = $3,
		 keywords = $4, topic = $5, persons = $6, event_time = $7, updated_at = $8
		 WHERE id = $9`,
		mem.Type, mem.Content, jsonOrNull(meta),
		jsonOrNull(mem.Keywords), mem.Topic, jsonOrNull(mem.Persons),
		mem.EventTime, mem.UpdatedAt, mem.ID,
	)
	if err != nil {
		return fmt.Errorf("memory: メモリの更新に失敗: %w", err)
	}
	return nil
}

// Delete は ID でメモリを削除する。
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("memory: メモリの削除に失敗: %w", err)
	}
	return nil
}

// DeleteBatch は複数 ID のメモリを一括削除し、削除件数を返す。
func (s *PostgresStore) DeleteBatch(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	res, err := s.db.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM memories WHERE id IN (%s)", strings.Join(placeholders, ",")),
		args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// VecStats は全メモリ数と embedding 済み件数を返す。
func (s *PostgresStore) VecStats(ctx context.Context) (total, embedded int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(embedding) FROM memories`,
	).Scan(&total, &embedded)
	return
}

// ListEmbeddedMemories は embedding が付与されたメモリ一覧を返す。
func (s *PostgresStore) ListEmbeddedMemories(ctx context.Context) ([]Memory, error) {
	q := fmt.Sprintf(`SELECT %s FROM memories WHERE embedding IS NOT NULL ORDER BY type, created_at`, pgMemColumns)
	return s.queryMems(ctx, q)
}

// ListAllEmbeddings は全メモリの ID と embedding ベクトルを返す。
func (s *PostgresStore) ListAllEmbeddings(ctx context.Context) (map[string][]float32, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, embedding FROM memories WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]float32)
	for rows.Next() {
		var id string
		var vec pgvector.Vector
		if err := rows.Scan(&id, &vec); err != nil {
			return nil, err
		}
		result[id] = vec.Slice()
	}
	return result, rows.Err()
}

// FindDuplicates は embedding の近さで重複グループを検出する。
func (s *PostgresStore) FindDuplicates(ctx context.Context, k int, threshold float64) ([]DuplicateGroup, error) {
	// pgvector で全ペアの distance を計算し、threshold 以下をグルーピング
	rows, err := s.db.QueryContext(ctx,
		`SELECT m1.id, m2.id, m1.embedding <=> m2.embedding AS dist
		 FROM memories m1
		 JOIN memories m2 ON m1.id < m2.id
		 WHERE m1.embedding IS NOT NULL AND m2.embedding IS NOT NULL
		   AND m1.embedding <=> m2.embedding < $1
		 ORDER BY dist
		 LIMIT $2`,
		threshold, k*100,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groupMap := make(map[string][]string)
	for rows.Next() {
		var id1, id2 string
		var dist float64
		if err := rows.Scan(&id1, &id2, &dist); err != nil {
			return nil, err
		}
		key := id1
		groupMap[key] = append(groupMap[key], id2)
	}

	var groups []DuplicateGroup
	for leader, members := range groupMap {
		allIDs := append([]string{leader}, members...)
		mems, err := s.loadMemoriesByIDs(ctx, allIDs)
		if err != nil {
			return nil, err
		}
		var g DuplicateGroup
		for _, id := range allIDs {
			if m, ok := mems[id]; ok {
				g.Memories = append(g.Memories, m)
			}
		}
		if len(g.Memories) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

func (s *PostgresStore) loadMemoriesByIDs(ctx context.Context, ids []string) (map[string]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT %s FROM memories WHERE id IN (%s)`,
		pgMemColumns, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]Memory)
	for rows.Next() {
		m, err := scanPGMem(rows)
		if err != nil {
			return nil, err
		}
		result[m.ID] = m
	}
	return result, rows.Err()
}

// BackfillEmbeddings は embedding が未設定のメモリにベクトルを付与する。
func (s *PostgresStore) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if s.embedder == nil {
		return 0, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content, metadata FROM memories
		 WHERE embedding IS NULL
		 LIMIT $1`, batchSize)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type pending struct {
		id      string
		content string
		meta    string
	}
	var items []pending
	for rows.Next() {
		var p pending
		var metaNull sql.NullString
		if err := rows.Scan(&p.id, &p.content, &metaNull); err != nil {
			return 0, err
		}
		if metaNull.Valid {
			p.meta = metaNull.String
		}
		items = append(items, p)
	}
	if len(items) == 0 {
		return 0, nil
	}

	inputs := make([][]embedding.Part, len(items))
	for i, item := range items {
		inputs[i] = []embedding.Part{embedding.TextPart(item.content)}
	}

	embeddings, err := s.embedder.EmbedBatch(ctx, inputs)
	if err != nil {
		return 0, fmt.Errorf("memory: batch embed に失敗: %w", err)
	}

	for i, emb := range embeddings {
		if len(emb) == 0 {
			continue
		}
		_, err := s.db.ExecContext(ctx,
			`UPDATE memories SET embedding = $1 WHERE id = $2`,
			pgvector.NewVector(emb), items[i].id)
		if err != nil {
			s.logger.Warn("embedding 更新に失敗", "id", items[i].id, "error", err)
		}
	}

	return len(items), nil
}

// RunEmbeddingWorker はバックグラウンドで embedding 未設定のメモリを処理する。
func (s *PostgresStore) RunEmbeddingWorker(ctx context.Context) {
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

		for {
			n, err := s.BackfillEmbeddings(ctx, batchSize)
			if err != nil {
				if backoff == 0 {
					backoff = time.Second
				} else {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				s.logger.Warn("backfill エラー、リトライ待ち", "backoff", backoff, "error", err)
				break
			}
			backoff = 0
			if n == 0 {
				break
			}
		}
	}
}
