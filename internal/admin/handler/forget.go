package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
)

// ForgetHandler provides HTTP handlers for memory deduplication management.
type ForgetHandler struct {
	memStore memory.AdminStore
	agentAPI string // e.g. "http://agent:9090"
	logger   *slog.Logger
}

// NewForgetHandler creates a new ForgetHandler.
func NewForgetHandler(memStore memory.AdminStore, agentAPI string, logger *slog.Logger) *ForgetHandler {
	return &ForgetHandler{memStore: memStore, agentAPI: agentAPI, logger: logger}
}

type forgetMemory struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type forgetGroup struct {
	Type     string         `json:"type"`
	Members  []forgetMemory `json:"members"`
	Distance float64        `json:"avg_distance"`
}

// Groups handles GET /api/forget/groups.
// Returns groups of similar memories found by Union-Find with transitive grouping.
func (h *ForgetHandler) Groups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	threshold := 0.3
	if t := r.URL.Query().Get("threshold"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = v
		}
	}

	// Load all memories with embeddings.
	type entry struct {
		id        string
		memType   string
		content   string
		metadata  map[string]any
		createdAt string
		updatedAt string
	}
	rows, err := h.memStore.DB().QueryContext(ctx,
		`SELECT v.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.created_at`)
	if err != nil {
		h.logger.Error("忘却: メモリ一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var all []entry
	for rows.Next() {
		var e entry
		var metaJSON sql.NullString
		if err := rows.Scan(&e.id, &e.memType, &e.content, &metaJSON, &e.createdAt, &e.updatedAt); err != nil {
			continue
		}
		if metaJSON.Valid && metaJSON.String != "" {
			_ = json.Unmarshal([]byte(metaJSON.String), &e.metadata)
		}
		all = append(all, e)
	}

	if len(all) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"data": []forgetGroup{}, "total": 0})
		return
	}

	// Build index.
	idxByID := make(map[string]int, len(all))
	for i, e := range all {
		idxByID[e.id] = i
	}

	// Union-Find grouping.
	parent := make([]int, len(all))
	rank := make([]int, len(all))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(x, y int) {
		rx, ry := find(x), find(y)
		if rx == ry {
			return
		}
		if rank[rx] < rank[ry] {
			rx, ry = ry, rx
		}
		parent[ry] = rx
		if rank[rx] == rank[ry] {
			rank[rx]++
		}
	}

	// Track distances for average calculation.
	type distPair struct{ i, j int; d float64 }
	var pairs []distPair

	for i, e := range all {
		neighRows, err := h.memStore.DB().QueryContext(ctx,
			`SELECT v2.id, v2.distance
			 FROM memories_vec v1
			 JOIN memories_vec v2 ON v2.embedding MATCH v1.embedding AND v2.k = 10
			 WHERE v1.id = ?`, e.id)
		if err != nil {
			continue
		}
		for neighRows.Next() {
			var nid string
			var dist float64
			if err := neighRows.Scan(&nid, &dist); err != nil {
				continue
			}
			if nid == e.id || dist >= threshold {
				continue
			}
			j, ok := idxByID[nid]
			if !ok || all[i].memType != all[j].memType {
				continue
			}
			union(i, j)
			pairs = append(pairs, distPair{i, j, dist})
		}
		neighRows.Close()
	}

	// Extract groups.
	groupMap := make(map[int][]int)
	for i := range all {
		root := find(i)
		groupMap[root] = append(groupMap[root], i)
	}

	// Calculate average distance per group.
	groupDists := make(map[int][]float64)
	for _, p := range pairs {
		root := find(p.i)
		groupDists[root] = append(groupDists[root], p.d)
	}

	var groups []forgetGroup
	for root, members := range groupMap {
		if len(members) < 2 {
			continue
		}
		g := forgetGroup{Type: all[members[0]].memType}
		for _, idx := range members {
			e := all[idx]
			g.Members = append(g.Members, forgetMemory{
				ID:        e.id,
				Type:      e.memType,
				Content:   e.content,
				Metadata:  e.metadata,
				CreatedAt: e.createdAt,
				UpdatedAt: e.updatedAt,
			})
		}
		if dists := groupDists[root]; len(dists) > 0 {
			var sum float64
			for _, d := range dists {
				sum += d
			}
			g.Distance = sum / float64(len(dists))
		}
		groups = append(groups, g)
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": groups, "total": len(groups)})
}

// Delete handles POST /api/forget/delete.
// Deletes specified memories (manual dedup from UI).
func (h *ForgetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeleteIDs []string `json:"delete_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if len(body.DeleteIDs) == 0 {
		http.Error(w, `{"error":"delete_ids is required"}`, http.StatusBadRequest)
		return
	}

	deleted, err := h.memStore.DeleteBatch(r.Context(), body.DeleteIDs)
	if err != nil {
		h.logger.Error("忘却: 削除に失敗", "error", err)
		http.Error(w, `{"error":"delete failed"}`, http.StatusInternalServerError)
		return
	}

	h.logger.Info("忘却: 手動削除を実行", "requested", len(body.DeleteIDs), "deleted", deleted)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// Merge handles POST /api/forget/merge.
// Deletes all specified IDs and creates a new memory with merged content.
func (h *ForgetHandler) Merge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeleteIDs     []string           `json:"delete_ids"`
		MergedContent string             `json:"merged_content"`
		Type          memory.MemoryType  `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if len(body.DeleteIDs) == 0 || body.MergedContent == "" || body.Type == "" {
		http.Error(w, `{"error":"delete_ids, merged_content, and type are required"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Delete originals.
	deleted, err := h.memStore.DeleteBatch(ctx, body.DeleteIDs)
	if err != nil {
		h.logger.Error("忘却: マージ時の削除に失敗", "error", err)
	}

	// Insert merged memory (without embedding — BackfillEmbeddings will handle it).
	mem := &memory.Memory{
		Type:    body.Type,
		Content: body.MergedContent,
		Metadata: map[string]any{
			"source": "forget_merge",
		},
	}
	if saveErr := h.memStore.SaveRaw(ctx, mem); saveErr != nil {
		h.logger.Error("忘却: マージ後の挿入に失敗", "error", saveErr)
		http.Error(w, `{"error":"merge insert failed"}`, http.StatusInternalServerError)
		return
	}

	h.logger.Info("忘却: 手動マージを実行", "deleted", deleted, "merged_content_len", len(body.MergedContent))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "merged": true})
}

// Status handles GET /api/forget/status.
// Returns the last run status from task_state.
func (h *ForgetHandler) Status(w http.ResponseWriter, r *http.Request) {
	var stateJSON string
	err := h.memStore.DB().QueryRowContext(r.Context(),
		`SELECT state FROM task_state WHERE task_name = 'forget'`,
	).Scan(&stateJSON)

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"has_run": false})
		return
	}

	var state map[string]any
	if json.Unmarshal([]byte(stateJSON), &state) != nil {
		writeJSON(w, http.StatusOK, map[string]any{"has_run": false})
		return
	}
	state["has_run"] = true
	writeJSON(w, http.StatusOK, state)
}

// Run handles POST /api/forget/run.
// Proxies to the agent's trigger API to run the forget task immediately.
func (h *ForgetHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.agentAPI == "" {
		http.Error(w, `{"error":"agent_api not configured"}`, http.StatusServiceUnavailable)
		return
	}

	url := strings.TrimSuffix(h.agentAPI, "/") + "/internal/trigger/forget"
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, strings.NewReader(`{}`))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Error("忘却: トリガーのプロキシに失敗", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
