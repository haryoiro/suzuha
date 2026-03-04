package forget

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Task implements scheduler.CronTask for periodic memory deduplication.
type Task struct {
	mu        sync.Mutex
	lastRunAt time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "forget" }
func (t *Task) Description() string { return "類似記憶の重複排除・統合" }

func (t *Task) Setup(ctx context.Context, cc *scheduler.CronContext) error {
	if cc.DB == nil {
		return nil
	}
	var s persistedState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("forget: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.lastRunAt = s.LastRunAt
	t.mu.Unlock()
	return nil
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	fc := defaultConfig()
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &fc); err != nil {
			cc.Logger.Warn("forget: parse config", "error", err)
		}
	}

	cc.Logger.Info("forget: starting",
		"similarity_threshold", fc.SimilarityThreshold,
		"dry_run", fc.DryRun)

	// Phase 1: Load all memories that have embeddings.
	entries, err := loadEntries(ctx, cc.DB)
	if err != nil {
		return fmt.Errorf("forget: load entries: %w", err)
	}
	if len(entries) < 2 {
		cc.Logger.Info("forget: not enough memories to deduplicate", "count", len(entries))
		return nil
	}
	cc.Logger.Info("forget: loaded memories", "count", len(entries))

	// Phase 2: Build similarity groups using Union-Find.
	// Load all embeddings in a single query instead of per-entry KNN queries.
	embeddings, embErr := loadAllEmbeddings(ctx, cc.DB)
	if embErr != nil {
		return fmt.Errorf("forget: load embeddings: %w", embErr)
	}

	uf := newUnionFind(len(entries))

	// Entries are sorted by type (ORDER BY m.type from loadEntries).
	// Compare within contiguous same-type blocks only.
	typeStart := 0
	for typeStart < len(entries) {
		typeEnd := typeStart + 1
		for typeEnd < len(entries) && entries[typeEnd].memType == entries[typeStart].memType {
			typeEnd++
		}
		for i := typeStart; i < typeEnd; i++ {
			embI, okI := embeddings[entries[i].id]
			if !okI {
				continue
			}
			for j := i + 1; j < typeEnd; j++ {
				embJ, okJ := embeddings[entries[j].id]
				if !okJ {
					continue
				}
				if cosineDistance(embI, embJ) < fc.SimilarityThreshold {
					uf.union(i, j)
				}
			}
		}
		typeStart = typeEnd
	}

	// Phase 3: Extract groups with 2+ members.
	rawGroups := uf.groups()
	var groups []memoryGroup
	for _, memberIndices := range rawGroups {
		g := memoryGroup{memType: entries[memberIndices[0]].memType}
		for _, idx := range memberIndices {
			g.members = append(g.members, entries[idx])
		}
		// Sort oldest first so LLM sees chronological order.
		sort.Slice(g.members, func(i, j int) bool {
			return g.members[i].createdAt.Before(g.members[j].createdAt)
		})
		// Cap group size.
		if len(g.members) > fc.MaxGroupSize {
			g.members = g.members[:fc.MaxGroupSize]
		}
		groups = append(groups, g)
	}

	if len(groups) == 0 {
		cc.Logger.Info("forget: no duplicate groups found")
		return nil
	}

	// Sort by group size descending (larger groups first).
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].members) > len(groups[j].members)
	})

	cc.Logger.Info("forget: found duplicate groups", "groups", len(groups))

	// Phase 4: Batch LLM calls to judge each group.
	var totalDeleted, totalMerged int

	for batchStart := 0; batchStart < len(groups); batchStart += fc.MaxGroupsPerLLMCall {
		batchEnd := batchStart + fc.MaxGroupsPerLLMCall
		if batchEnd > len(groups) {
			batchEnd = len(groups)
		}
		batch := groups[batchStart:batchEnd]

		decisions, err := judgeBatch(ctx, cc, batch)
		if err != nil {
			cc.Logger.Error("forget: llm judge batch", "error", err,
				"batch", fmt.Sprintf("%d-%d", batchStart, batchEnd))
			continue
		}

		// Phase 5: Execute decisions.
		for _, d := range decisions {
			if fc.DryRun {
				cc.Logger.Info("forget: [dry-run]",
					"action", d.action,
					"keep", d.keepID,
					"delete", d.deleteIDs,
					"reason", d.reason)
				continue
			}

			switch d.action {
			case "keep":
				n, err := cc.MemoryAdmin.DeleteBatch(ctx, d.deleteIDs)
				if err != nil {
					cc.Logger.Error("forget: delete", "error", err, "ids", d.deleteIDs)
				}
				totalDeleted += n
			case "merge":
				if err := mergeMemories(ctx, cc, d); err != nil {
					cc.Logger.Error("forget: merge", "error", err)
				} else {
					totalMerged++
					totalDeleted += len(d.deleteIDs)
				}
			}
		}
	}

	cc.Logger.Info("forget: completed",
		"groups", len(groups),
		"deleted", totalDeleted,
		"merged", totalMerged,
		"dry_run", fc.DryRun)

	// Phase 6: Persist state.
	t.mu.Lock()
	t.lastRunAt = time.Now()
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &persistedState{
		LastRunAt:    time.Now(),
		TotalDeleted: totalDeleted,
		TotalMerged:  totalMerged,
	}); err != nil {
		cc.Logger.Warn("forget: save state", "error", err)
	}

	return nil
}

// --- config & state ---

type forgetConfig struct {
	SimilarityThreshold float64 `json:"similarity_threshold"`
	KNNNeighbours       int     `json:"knn_neighbors"`
	MaxGroupsPerLLMCall int     `json:"max_groups_per_llm_call"`
	MaxGroupSize        int     `json:"max_group_size"`
	DryRun              bool    `json:"dry_run"`
}

func defaultConfig() forgetConfig {
	return forgetConfig{
		SimilarityThreshold: 0.3,
		KNNNeighbours:       10,
		MaxGroupsPerLLMCall: 5,
		MaxGroupSize:        8,
		DryRun:              false,
	}
}

type persistedState struct {
	LastRunAt    time.Time `json:"last_run_at"`
	TotalDeleted int       `json:"total_deleted"`
	TotalMerged  int       `json:"total_merged"`
}

// --- data types ---

type memEntry struct {
	id        string
	memType   memory.MemoryType
	content   string
	metadata  map[string]any
	createdAt time.Time
}

type memoryGroup struct {
	memType memory.MemoryType
	members []memEntry
}

type decision struct {
	action        string // "keep" or "merge"
	keepID        string
	deleteIDs     []string
	mergedContent string
	groupType     memory.MemoryType
	reason        string
}

// --- DB helpers ---

// deserializeFloat32 converts a raw byte blob (little-endian packed float32s) to []float32.
func deserializeFloat32(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("invalid blob length %d", len(blob))
	}
	n := len(blob) / 4
	vec := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(blob[i*4 : (i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec, nil
}

// cosineDistance returns the cosine distance between two vectors (0=identical, 2=opposite).
func cosineDistance(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 2.0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 2.0
	}
	return 1.0 - dot/denom
}

// loadAllEmbeddings loads all embeddings from memories_vec in a single query.
func loadAllEmbeddings(ctx context.Context, db *sql.DB) (map[string][]float32, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, embedding FROM memories_vec`)
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]float32)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec, err := deserializeFloat32(blob)
		if err != nil {
			continue
		}
		result[id] = vec
	}
	return result, rows.Err()
}

func loadEntries(ctx context.Context, db *sql.DB) ([]memEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT v.id, m.type, m.content, m.metadata, m.created_at
		 FROM memories_vec v
		 JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []memEntry
	for rows.Next() {
		var e memEntry
		var typ string
		var metaRaw sql.NullString
		if err := rows.Scan(&e.id, &typ, &e.content, &metaRaw, &e.createdAt); err != nil {
			return nil, err
		}
		e.memType = memory.MemoryType(typ)
		if metaRaw.Valid && metaRaw.String != "" {
			if err := json.Unmarshal([]byte(metaRaw.String), &e.metadata); err != nil {
				slog.Warn("forget: unmarshal metadata", "id", e.id, "error", err)
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}



// mergeMemories deletes all group members and saves a new memory with merged content.
func mergeMemories(ctx context.Context, cc *scheduler.CronContext, d decision) error {
	// Delete all original members via AdminStore.
	if _, err := cc.MemoryAdmin.DeleteBatch(ctx, d.deleteIDs); err != nil {
		cc.Logger.Warn("forget: merge delete", "error", err, "ids", d.deleteIDs)
	}

	// Save new merged memory (embedding auto-generated by Store.Save).
	mem := &memory.Memory{
		Type:    d.groupType,
		Content: d.mergedContent,
		Metadata: map[string]any{
			"source": "forget_merge",
		},
	}
	return cc.Memory.Save(ctx, mem)
}

// --- LLM judgment ---

func judgeBatch(ctx context.Context, cc *scheduler.CronContext, groups []memoryGroup) ([]decision, error) {
	prompt := buildJudgePrompt(groups)

	resp, err := cc.LLM.CompleteRawDefault(ctx, []providers.Message{
		{Role: "system", Content: judgeSystemPrompt},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	return parseDecisions(resp.Text, groups)
}

const judgeSystemPrompt = `あなたは記憶の管理者です。類似した記憶のグループを評価し、重複を判定してください。
同じ事柄を別の言い回しで記録したものは重複です。
異なる時点の出来事や、補完し合う情報を含む記憶は統合（merge）してください。
類似しているが別の事柄である場合は skip してください。`

func buildJudgePrompt(groups []memoryGroup) string {
	var sb strings.Builder

	sb.WriteString("以下の記憶グループを評価してください。\n\n")

	for gi, g := range groups {
		fmt.Fprintf(&sb, "=== グループ %d（型: %s, %d件）===\n", gi+1, g.memType, len(g.members))
		for mi, m := range g.members {
			age := time.Since(m.createdAt).Hours() / 24
			content := truncateRunes(m.content, 200)
			fmt.Fprintf(&sb, "[%d-%d] id=%s (%.0f日前)\n%s\n\n", gi+1, mi+1, m.id, age, content)
		}
	}

	sb.WriteString("JSON配列で返してください（これだけ出力して）:\n```\n")
	sb.WriteString(`[{"group":1,"action":"keep|merge|skip","keep_id":"残すID","merged_content":"統合内容(200字以内)","reason":"理由"}]`)
	sb.WriteString("\n```\n")
	sb.WriteString("- keep: keep_id に残す記憶の ID を指定\n")
	sb.WriteString("- merge: merged_content に統合後の内容を記述\n")
	sb.WriteString("- skip: 変更なし\n")

	return sb.String()
}

type llmDecision struct {
	Group         int    `json:"group"`
	Action        string `json:"action"`
	KeepID        string `json:"keep_id"`
	MergedContent string `json:"merged_content"`
	Reason        string `json:"reason"`
}

func parseDecisions(raw string, groups []memoryGroup) ([]decision, error) {
	raw = stripCodeFence(strings.TrimSpace(raw))

	var llmDecs []llmDecision
	if err := json.Unmarshal([]byte(raw), &llmDecs); err != nil {
		return nil, fmt.Errorf("parse: %w (raw: %s)", err, truncateRunes(raw, 200))
	}

	var decisions []decision
	for _, ld := range llmDecs {
		if ld.Group < 1 || ld.Group > len(groups) {
			continue
		}
		g := groups[ld.Group-1]

		switch ld.Action {
		case "keep":
			if ld.KeepID == "" {
				continue
			}
			// Verify the keep_id belongs to this group.
			found := false
			var delIDs []string
			for _, m := range g.members {
				if m.id == ld.KeepID {
					found = true
				} else {
					delIDs = append(delIDs, m.id)
				}
			}
			if !found || len(delIDs) == 0 {
				continue
			}
			decisions = append(decisions, decision{
				action:    "keep",
				keepID:    ld.KeepID,
				deleteIDs: delIDs,
				groupType: g.memType,
				reason:    ld.Reason,
			})

		case "merge":
			if ld.MergedContent == "" {
				continue
			}
			var allIDs []string
			for _, m := range g.members {
				allIDs = append(allIDs, m.id)
			}
			decisions = append(decisions, decision{
				action:        "merge",
				deleteIDs:     allIDs,
				mergedContent: ld.MergedContent,
				groupType:     g.memType,
				reason:        ld.Reason,
			})
		}
	}
	return decisions, nil
}

// --- helpers ---

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func stripCodeFence(s string) string {
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
