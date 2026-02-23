package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/haryoiro/suzuha/internal/memory"
)

// MemoryHandler provides HTTP handlers for memory CRUD operations.
type MemoryHandler struct {
	store  memory.AdminStore
	logger *slog.Logger
}

// NewMemoryHandler creates a new MemoryHandler.
func NewMemoryHandler(store memory.AdminStore, logger *slog.Logger) *MemoryHandler {
	return &MemoryHandler{store: store, logger: logger}
}

type listResponse struct {
	Data  []memory.Memory `json:"data"`
	Total int             `json:"total"`
}

type singleResponse struct {
	Data *memory.Memory `json:"data"`
}

// List handles GET /api/memories with pagination and filtering.
func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 20
	}

	opts := memory.ListOpts{
		Offset:   offset,
		Limit:    limit,
		Type:     memory.MemoryType(q.Get("type")),
		Query:    q.Get("q"),
		OrderBy:  q.Get("order"),
		OrderDir: q.Get("dir"),
	}

	memories, total, err := h.store.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("list memories", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if memories == nil {
		memories = []memory.Memory{}
	}

	writeJSON(w, http.StatusOK, listResponse{Data: memories, Total: total})
}

type createRequest struct {
	Type     memory.MemoryType `json:"type"`
	Content  string            `json:"content"`
	Metadata map[string]any    `json:"metadata"`
}

// Create handles POST /api/memories.
func (h *MemoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	mem := &memory.Memory{
		Type:     req.Type,
		Content:  req.Content,
		Metadata: req.Metadata,
	}
	if err := h.store.Save(r.Context(), mem); err != nil {
		h.logger.Error("create memory", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, singleResponse{Data: mem})
}

// Get handles GET /api/memories/{id}.
func (h *MemoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mem, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.logger.Error("get memory", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, singleResponse{Data: mem})
}

type updateRequest struct {
	Type     memory.MemoryType `json:"type"`
	Content  string            `json:"content"`
	Metadata map[string]any    `json:"metadata"`
}

// Update handles PUT /api/memories/{id}.
func (h *MemoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	mem := &memory.Memory{
		ID:       id,
		Type:     req.Type,
		Content:  req.Content,
		Metadata: req.Metadata,
	}
	if err := h.store.Update(r.Context(), mem); err != nil {
		h.logger.Error("update memory", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, singleResponse{Data: mem})
}

// Delete handles DELETE /api/memories/{id}.
func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.logger.Error("delete memory", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// VecStats handles GET /api/memories/vec-stats.
// Returns vector storage statistics: total memories, embedded count, dimensions.
func (h *MemoryHandler) VecStats(w http.ResponseWriter, r *http.Request) {
	db := h.store.DB()
	ctx := r.Context()

	var totalMemories int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories").Scan(&totalMemories)

	var embeddedCount int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memories_vec").Scan(&embeddedCount)

	// Get IDs with embeddings for the list endpoint.
	writeJSON(w, http.StatusOK, map[string]any{
		"total_memories":  totalMemories,
		"embedded_count":  embeddedCount,
		"missing_count":   totalMemories - embeddedCount,
		"coverage_pct":    safePct(embeddedCount, totalMemories),
	})
}

func safePct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// ListWithVec handles GET /api/memories/with-vec.
// Returns memories along with whether each has a vector embedding.
func (h *MemoryHandler) ListWithVec(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 20
	}

	opts := memory.ListOpts{
		Offset:   offset,
		Limit:    limit,
		Type:     memory.MemoryType(q.Get("type")),
		Query:    q.Get("q"),
		OrderBy:  q.Get("order"),
		OrderDir: q.Get("dir"),
	}

	memories, total, err := h.store.List(r.Context(), opts)
	if err != nil {
		h.logger.Error("list memories with vec", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if memories == nil {
		memories = []memory.Memory{}
	}

	// Look up which IDs have embeddings.
	embeddedSet := make(map[string]bool)
	if len(memories) > 0 {
		ids := make([]any, len(memories))
		placeholders := ""
		for i, m := range memories {
			ids[i] = m.ID
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
		}
		rows, err := h.store.DB().QueryContext(r.Context(),
			"SELECT id FROM memories_vec WHERE id IN ("+placeholders+")", ids...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id string
				rows.Scan(&id)
				embeddedSet[id] = true
			}
		}
	}

	type memWithVec struct {
		memory.Memory
		HasEmbedding bool `json:"has_embedding"`
	}

	result := make([]memWithVec, len(memories))
	for i, m := range memories {
		result[i] = memWithVec{Memory: m, HasEmbedding: embeddedSet[m.ID]}
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": result, "total": total})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
