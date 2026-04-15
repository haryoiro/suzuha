//go:build sqlite

package memory

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/haryoiro/suzuha/external/embedding"
)

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
		group := func() []Memory {
			neighRows, err := s.db.QueryContext(ctx,
				`SELECT v2.id, v2.distance
				 FROM memories_vec v1
				 JOIN memories_vec v2 ON v2.embedding MATCH v1.embedding AND v2.k = ?
				 WHERE v1.id = ?`, k, all[i].ID)
			if err != nil {
				return nil
			}
			defer neighRows.Close()

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
			return group
		}()

		if len(group) > 1 {
			groups = append(groups, DuplicateGroup{Memories: group})
		}
	}
	return groups, nil
}

