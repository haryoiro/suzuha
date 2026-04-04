package location

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// Handler receives Overland GPS data via HTTP POST.
type Handler struct {
	store  *Store
	token  string
	logger *slog.Logger
}

// NewHandler creates an Overland webhook handler.
func NewHandler(store *Store, token string, logger *slog.Logger) *Handler {
	return &Handler{store: store, token: token, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Validate Bearer token.
	if h.token != "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != h.token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	}

	var payload OverlandPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.logger.Warn("overland: ペイロードが不正です", "error", err)
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	locs := ParseOverlandPayload(&payload)
	if len(locs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"result":"ok"}`))
		return
	}

	if err := h.store.SaveBatch(r.Context(), locs); err != nil {
		h.logger.Error("overland: 保存に失敗しました", "error", err, "count", len(locs))
		http.Error(w, `{"error":"save failed"}`, http.StatusInternalServerError)
		return
	}

	h.logger.Debug("overland: 位置情報を保存しました", "count", len(locs))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"result":"ok"}`))
}
