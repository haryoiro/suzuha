package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pgvector/pgvector-go"
)

// PostgresStore は ParadeDB (PostgreSQL + pgvector + pg_search) を使った Store 実装。
type PostgresStore struct {
	db         *sql.DB
	embedder   embedding.Embedder
	mediaStore MediaStore
	onSave     func()
	logger     *slog.Logger
	embedSig   chan struct{}
}

// NewPostgresStore は ParadeDB に接続し、マイグレーションを実行する。
func NewPostgresStore(dsn string, embedder embedding.Embedder, runMigrations bool, logger *slog.Logger) (*PostgresStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: PostgreSQL 接続に失敗: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: PostgreSQL ping に失敗: %w", err)
	}

	if runMigrations {
		if err := migratePostgres(db); err != nil {
			db.Close()
			return nil, err
		}
	}

	return &PostgresStore{
		db:       db,
		embedder: embedder,
		logger:   logger,
		embedSig: make(chan struct{}, 1),
	}, nil
}

func (s *PostgresStore) SetMediaStore(ms MediaStore) { s.mediaStore = ms }
func (s *PostgresStore) SetOnSave(fn func())         { s.onSave = fn }

func (s *PostgresStore) DB() *sql.DB { return s.db }

func (s *PostgresStore) Close() error { return s.db.Close() }

// truncateAll はテスト用に全データを削除する。
func (s *PostgresStore) truncateAll(ctx context.Context) {
	s.db.ExecContext(ctx, "TRUNCATE memories CASCADE")
}

const pgMemColumns = "id, type, content, embedding, metadata, keywords, topic, persons, event_time, created_at, updated_at"

func scanPGMem(row interface{ Scan(dest ...any) error }) (Memory, error) {
	var m Memory
	var metaJSON, keywordsJSON, personsJSON sql.NullString
	var eventTime sql.NullTime
	var embBytes []byte

	err := row.Scan(
		&m.ID, &m.Type, &m.Content, &embBytes,
		&metaJSON, &keywordsJSON, &m.Topic, &personsJSON,
		&eventTime, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return m, err
	}

	if metaJSON.Valid && metaJSON.String != "" {
		json.Unmarshal([]byte(metaJSON.String), &m.Metadata)
		if atts, ok := m.Metadata["attachments"]; ok {
			if raw, err := json.Marshal(atts); err == nil {
				json.Unmarshal(raw, &m.Attachments)
			}
		}
	}
	if keywordsJSON.Valid && keywordsJSON.String != "" {
		json.Unmarshal([]byte(keywordsJSON.String), &m.Keywords)
	}
	if personsJSON.Valid && personsJSON.String != "" {
		json.Unmarshal([]byte(personsJSON.String), &m.Persons)
	}
	if eventTime.Valid {
		m.EventTime = &eventTime.Time
	}

	return m, nil
}

func (s *PostgresStore) initMemFields(mem *Memory) {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := jtime.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now
}

func jsonOrNull(v any) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(data), Valid: true}
}

func (s *PostgresStore) Save(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	meta := mem.Metadata
	if len(mem.Attachments) > 0 {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["attachments"] = mem.Attachments
	}

	var embValue any
	if len(mem.Embedding) > 0 {
		embValue = pgvector.NewVector(mem.Embedding)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, type, content, embedding, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type, content = EXCLUDED.content, embedding = EXCLUDED.embedding,
		   metadata = EXCLUDED.metadata, keywords = EXCLUDED.keywords, topic = EXCLUDED.topic,
		   persons = EXCLUDED.persons, event_time = EXCLUDED.event_time, updated_at = EXCLUDED.updated_at`,
		mem.ID, mem.Type, mem.Content, embValue,
		jsonOrNull(meta), jsonOrNull(mem.Keywords), mem.Topic, jsonOrNull(mem.Persons),
		mem.EventTime, mem.CreatedAt, mem.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("memory: save に失敗: %w", err)
	}

	if s.onSave != nil {
		s.onSave()
	}
	if len(mem.Embedding) == 0 {
		s.notifyEmbedWorker()
	}
	return nil
}

func (s *PostgresStore) notifyEmbedWorker() {
	select {
	case s.embedSig <- struct{}{}:
	default:
	}
}

// Search は BM25 (pg_search) でテキスト検索する。
// embedder がある場合はベクトル検索も行い RRF でマージする。
func (s *PostgresStore) Search(ctx context.Context, query string, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, time.Time{})
}

func (s *PostgresStore) SearchWithContext(ctx context.Context, query string, limit int, filter SymbolicFilter) ([]Memory, error) {
	// TODO: Symbolic filter 対応
	return s.searchInternal(ctx, query, "", limit, time.Time{})
}

func (s *PostgresStore) SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error) {
	return s.searchInternal(ctx, query, memType, limit, time.Time{})
}

func (s *PostgresStore) SearchRecent(ctx context.Context, query string, limit int, since time.Time) ([]Memory, error) {
	return s.searchInternal(ctx, query, "", limit, since)
}

func (s *PostgresStore) searchInternal(ctx context.Context, query string, memType MemoryType, limit int, since time.Time) ([]Memory, error) {
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

func (s *PostgresStore) SearchByParts(ctx context.Context, parts []embedding.Part, limit int) ([]Memory, error) {
	if s.embedder == nil {
		return nil, nil
	}
	emb, err := s.embedder.Embed(ctx, parts)
	if err != nil {
		return nil, fmt.Errorf("memory: embedding に失敗: %w", err)
	}
	return s.searchVec(ctx, emb, "", limit)
}

func (s *PostgresStore) searchVec(ctx context.Context, emb []float32, memType MemoryType, limit int) ([]Memory, error) {
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

func (s *PostgresStore) ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = 'user' AND metadata->>'user_id' = $1
		 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, userID, limit)
}

func (s *PostgresStore) ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = 'episode' AND persons ? $1
		 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, userID, limit)
}

func (s *PostgresStore) ListByType(ctx context.Context, memType MemoryType, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = $1 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, string(memType), limit)
}

func (s *PostgresStore) ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = $1 AND created_at >= $2
		 ORDER BY created_at DESC LIMIT $3`, pgMemColumns)
	return s.queryMems(ctx, q, string(memType), since, limit)
}

func (s *PostgresStore) ListRecent(ctx context.Context, since time.Time, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE created_at >= $1
		 ORDER BY created_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, since, limit)
}

func (s *PostgresStore) queryMems(ctx context.Context, query string, args ...any) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		m, err := scanPGMem(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

func (s *PostgresStore) IsDuplicate(ctx context.Context, content string, memType MemoryType) (string, []float32, error) {
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

func (s *PostgresStore) IsDuplicateBatch(ctx context.Context, candidates []DupCandidate) ([]DupResult, error) {
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

// AdminStore methods

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
	return err
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = $1`, id)
	return err
}

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

func (s *PostgresStore) SaveRaw(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)
	meta := mem.Metadata
	if len(mem.Attachments) > 0 {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["attachments"] = mem.Attachments
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type, content = EXCLUDED.content,
		   metadata = EXCLUDED.metadata, keywords = EXCLUDED.keywords, topic = EXCLUDED.topic,
		   persons = EXCLUDED.persons, event_time = EXCLUDED.event_time, updated_at = EXCLUDED.updated_at`,
		mem.ID, mem.Type, mem.Content,
		jsonOrNull(meta), jsonOrNull(mem.Keywords), mem.Topic, jsonOrNull(mem.Persons),
		mem.EventTime, mem.CreatedAt, mem.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) VecStats(ctx context.Context) (total, embedded int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(embedding) FROM memories`,
	).Scan(&total, &embedded)
	return
}

func (s *PostgresStore) ListEmbeddedMemories(ctx context.Context) ([]Memory, error) {
	q := fmt.Sprintf(`SELECT %s FROM memories WHERE embedding IS NOT NULL ORDER BY type, created_at`, pgMemColumns)
	return s.queryMems(ctx, q)
}

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
