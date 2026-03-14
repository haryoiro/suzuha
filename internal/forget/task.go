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
		cc.Logger.Warn("forget: 状態の読み込みに失敗", "error", err)
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
			cc.Logger.Warn("forget: 設定の解析に失敗", "error", err)
		}
	}

	cc.Logger.Info("forget: 開始",
		"similarity_threshold", fc.SimilarityThreshold,
		"dry_run", fc.DryRun)

	// Phase 1: Load all memories that have embeddings.
	entries, err := loadEntries(ctx, cc.DB)
	if err != nil {
		return fmt.Errorf("forget: エントリの読み込みに失敗: %w", err)
	}
	if len(entries) < 2 {
		cc.Logger.Info("forget: 重複排除に必要な記憶数が不足", "count", len(entries))
		return nil
	}
	cc.Logger.Info("forget: 記憶を読み込み完了", "count", len(entries))

	// Phase 2: Build similarity groups using Union-Find.
	// Load all embeddings in a single query instead of per-entry KNN queries.
	embeddings, embErr := loadAllEmbeddings(ctx, cc.DB)
	if embErr != nil {
		return fmt.Errorf("forget: 埋め込みの読み込みに失敗: %w", embErr)
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
		cc.Logger.Info("forget: 重複グループは見つかりませんでした")
		return nil
	}

	// Sort by group size descending (larger groups first).
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].members) > len(groups[j].members)
	})

	cc.Logger.Info("forget: 重複グループを検出", "groups", len(groups))

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
			cc.Logger.Error("forget: LLM判定バッチでエラー", "error", err,
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
					cc.Logger.Error("forget: 削除に失敗", "error", err, "ids", d.deleteIDs)
				}
				totalDeleted += n
			case "merge":
				if err := mergeMemories(ctx, cc, d); err != nil {
					cc.Logger.Error("forget: 統合に失敗", "error", err)
				} else {
					totalMerged++
					totalDeleted += len(d.deleteIDs)
				}
			}
		}
	}

	cc.Logger.Info("forget: 完了",
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
		cc.Logger.Warn("forget: 状態の保存に失敗", "error", err)
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
	action         string // "keep" or "merge"
	keepID         string
	deleteIDs      []string
	mergedContent  string
	groupType      memory.MemoryType
	reason         string
	sourceMetadata []map[string]any // metadata from original members (merge only)
}

// --- DB helpers ---

// deserializeFloat32 converts a raw byte blob (little-endian packed float32s) to []float32.
func deserializeFloat32(blob []byte) ([]float32, error) {
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
		return nil, fmt.Errorf("埋め込みの読み込みに失敗: %w", err)
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
				slog.Warn("forget: メタデータのJSON解析に失敗", "id", e.id, "error", err)
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
		cc.Logger.Warn("forget: 統合時の削除に失敗", "error", err, "ids", d.deleteIDs)
	}

	// Save new merged memory (embedding auto-generated by Store.Save).
	mem := &memory.Memory{
		Type:     d.groupType,
		Content:  d.mergedContent,
		Metadata: mergeMetadata(d.sourceMetadata),
	}
	return cc.Memory.Save(ctx, mem)
}

// mergeMetadata combines metadata from multiple source memories.
// Preserves participants (union of all), emotional_tone (comma-joined),
// user_id, and marks the source as "forget_merge".
func mergeMetadata(sources []map[string]any) map[string]any {
	merged := map[string]any{"source": "forget_merge"}

	participantSet := make(map[string]bool)
	var tones []string
	var userID string

	for _, m := range sources {
		if m == nil {
			continue
		}
		// Collect participants.
		switch v := m["participants"].(type) {
		case []any:
			for _, p := range v {
				if s, ok := p.(string); ok && s != "" {
					participantSet[s] = true
				}
			}
		case []string:
			for _, s := range v {
				if s != "" {
					participantSet[s] = true
				}
			}
		}
		// Collect emotional_tone.
		if t, ok := m["emotional_tone"].(string); ok && t != "" {
			tones = append(tones, t)
		}
		// Keep first user_id found.
		if uid, ok := m["user_id"].(string); ok && uid != "" && userID == "" {
			userID = uid
		}
	}

	if len(participantSet) > 0 {
		participants := make([]string, 0, len(participantSet))
		for p := range participantSet {
			participants = append(participants, p)
		}
		sort.Strings(participants)
		merged["participants"] = participants
	}
	if len(tones) > 0 {
		merged["emotional_tone"] = strings.Join(tones, ",")
	}
	if userID != "" {
		merged["user_id"] = userID
	}

	return merged
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

判定ルール:
- keep: まったく同じ事柄を別の言い回しで記録したもの → 最も情報量が多いものを残す
- merge: 異なる時点だが同一の事柄・文脈で、統合すると情報が増える場合のみ
- skip: 以下に該当する場合は必ず skip にすること
  - 話題やキーワードが似ているだけで、具体的な内容が異なる
  - 同じ人物についてだが、別の事実や出来事を記録している
  - user_id や participants が異なる
  - 日付が大きく離れている（同じ話題でも別の機会の出来事）
  - 片方が一般的な事実、もう片方が特定のエピソード

重要: 迷ったら skip にしてください。誤って削除・統合すると情報が失われます。`

func buildJudgePrompt(groups []memoryGroup) string {
	var sb strings.Builder

	sb.WriteString("以下の記憶グループを評価してください。\n\n")

	for gi, g := range groups {
		fmt.Fprintf(&sb, "=== グループ %d（型: %s, %d件）===\n", gi+1, g.memType, len(g.members))
		for mi, m := range g.members {
			content := truncateRunes(m.content, 200)
			date := m.createdAt.Format("2006-01-02")
			meta := formatMetadata(m.metadata)
			if meta != "" {
				fmt.Fprintf(&sb, "[%d-%d] id=%s date=%s %s\n%s\n\n", gi+1, mi+1, m.id, date, meta, content)
			} else {
				fmt.Fprintf(&sb, "[%d-%d] id=%s date=%s\n%s\n\n", gi+1, mi+1, m.id, date, content)
			}
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
			var allMeta []map[string]any
			for _, m := range g.members {
				allIDs = append(allIDs, m.id)
				allMeta = append(allMeta, m.metadata)
			}
			decisions = append(decisions, decision{
				action:         "merge",
				deleteIDs:      allIDs,
				mergedContent:  ld.MergedContent,
				groupType:      g.memType,
				reason:         ld.Reason,
				sourceMetadata: allMeta,
			})
		}
	}
	return decisions, nil
}

// --- helpers ---

// formatMetadata extracts key metadata fields for LLM display.
func formatMetadata(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	var parts []string
	if uid, ok := meta["user_id"].(string); ok && uid != "" {
		parts = append(parts, "user_id="+uid)
	}
	switch v := meta["participants"].(type) {
	case []any:
		var ids []string
		for _, p := range v {
			if s, ok := p.(string); ok {
				ids = append(ids, s)
			}
		}
		if len(ids) > 0 {
			parts = append(parts, "participants="+strings.Join(ids, ","))
		}
	case []string:
		if len(v) > 0 {
			parts = append(parts, "participants="+strings.Join(v, ","))
		}
	}
	if tone, ok := meta["emotional_tone"].(string); ok && tone != "" {
		parts = append(parts, "tone="+tone)
	}
	return strings.Join(parts, " ")
}

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
