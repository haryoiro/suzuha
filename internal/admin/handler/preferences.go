package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/haryoiro/suzuha/internal/preferences"
)

// PreferencesHandler provides HTTP handlers for preferences management.
type PreferencesHandler struct {
	store  *preferences.Store
	db     *sql.DB
	logger *slog.Logger
}

// NewPreferencesHandler creates a new PreferencesHandler.
func NewPreferencesHandler(db *sql.DB, logger *slog.Logger) *PreferencesHandler {
	return &PreferencesHandler{store: preferences.NewStore(db), db: db, logger: logger}
}

// List handles GET /api/preferences.
func (h *PreferencesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stance := q.Get("stance")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}

	var prefs []preferences.Preference
	var err error
	if stance != "" && stance != "all" {
		prefs, err = h.store.ListByStance(r.Context(), preferences.Stance(stance), limit)
	} else {
		prefs, err = h.store.ListAll(r.Context(), limit)
	}
	if err != nil {
		h.logger.Error("好み一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if prefs == nil {
		prefs = []preferences.Preference{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": prefs, "total": len(prefs)})
}

type updatePreferenceRequest struct {
	Stance     *string  `json:"stance"`
	Confidence *float64 `json:"confidence"`
	Reasoning  *string  `json:"reasoning"`
}

// Update handles PUT /api/preferences/{id}.
func (h *PreferencesHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}

	var req updatePreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	// Read current values to fill in any missing fields.
	var curStance string
	var curConfidence float64
	var curReasoning string
	err = h.db.QueryRowContext(r.Context(),
		`SELECT stance, confidence, reasoning FROM preferences WHERE id = ?`, id,
	).Scan(&curStance, &curConfidence, &curReasoning)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	stance := curStance
	confidence := curConfidence
	reasoning := curReasoning
	if req.Stance != nil {
		stance = *req.Stance
	}
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	if req.Reasoning != nil {
		reasoning = *req.Reasoning
	}

	if err := h.store.MarkEvaluated(r.Context(), id, preferences.Stance(stance), confidence, reasoning); err != nil {
		h.logger.Error("好みの更新に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/preferences/{id}.
func (h *PreferencesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.logger.Error("好みの削除に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Stats handles GET /api/preferences/stats.
func (h *PreferencesHandler) Stats(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT stance, COUNT(*) FROM preferences GROUP BY stance`)
	if err != nil {
		h.logger.Error("好み統計の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	counts := map[string]int{
		"liked": 0, "disliked": 0, "curious": 0, "undecided": 0,
	}
	total := 0
	for rows.Next() {
		var stance string
		var count int
		if err := rows.Scan(&stance, &count); err == nil {
			counts[stance] = count
			total += count
		}
	}
	counts["total"] = total
	writeJSON(w, http.StatusOK, counts)
}
