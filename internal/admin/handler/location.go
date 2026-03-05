package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/location"
)

// LocationHandler provides HTTP handlers for location devices and places.
type LocationHandler struct {
	db        *sql.DB
	agentBase string
	logger    *slog.Logger
}

// NewLocationHandler creates a new LocationHandler.
func NewLocationHandler(db *sql.DB, agentBase string, logger *slog.Logger) *LocationHandler {
	return &LocationHandler{db: db, agentBase: agentBase, logger: logger}
}

func (h *LocationHandler) notifyAgentReload(r *http.Request) {
	if h.agentBase == "" {
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.agentBase+"/internal/reload-location-settings", nil)
	if err != nil {
		return
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		h.logger.Warn("位置情報設定の再読み込みプロキシに失敗", "error", err)
		return
	}
	resp.Body.Close()
}

// --- User Location ---

// GetLocation handles GET /api/location/{userId}.
// Returns the latest location for all devices linked to the given user ID,
// including any matching place (geofence).
func (h *LocationHandler) GetLocation(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	if userID == "" {
		http.Error(w, `{"error":"user_id required"}`, http.StatusBadRequest)
		return
	}

	store := location.NewStore(h.db)
	locs, err := store.QueryLatestByUserID(r.Context(), userID)
	if err != nil {
		h.logger.Error("ユーザー位置情報の取得に失敗", "user_id", userID, "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if locs == nil {
		locs = []location.UserLocation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": locs})
}

// --- Devices ---

// ListDevices handles GET /api/location/devices.
func (h *LocationHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	store := location.NewStore(h.db)
	devices, err := store.ListDevices(r.Context())
	if err != nil {
		h.logger.Error("位置情報デバイス一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if devices == nil {
		devices = []location.DeviceMapping{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": devices})
}

// UpsertDevice handles PUT /api/location/devices/{id}.
func (h *LocationHandler) UpsertDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	if deviceID == "" {
		http.Error(w, `{"error":"device_id required"}`, http.StatusBadRequest)
		return
	}

	var body struct {
		OwnerName string `json:"owner_name"`
		UserID    string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if body.OwnerName == "" && body.UserID == "" {
		http.Error(w, `{"error":"owner_name or user_id required"}`, http.StatusBadRequest)
		return
	}

	store := location.NewStore(h.db)
	if err := store.UpsertDevice(r.Context(), deviceID, body.OwnerName, body.UserID); err != nil {
		h.logger.Error("位置情報デバイスの登録・更新に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	h.notifyAgentReload(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteDevice handles DELETE /api/location/devices/{id}.
func (h *LocationHandler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	if deviceID == "" {
		http.Error(w, `{"error":"device_id required"}`, http.StatusBadRequest)
		return
	}

	store := location.NewStore(h.db)
	if err := store.DeleteDevice(r.Context(), deviceID); err != nil {
		h.logger.Error("位置情報デバイスの削除に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	h.notifyAgentReload(r)
	w.WriteHeader(http.StatusNoContent)
}

// --- Places ---

// ListPlaces handles GET /api/location/places.
func (h *LocationHandler) ListPlaces(w http.ResponseWriter, r *http.Request) {
	store := location.NewStore(h.db)
	places, err := store.ListPlaces(r.Context())
	if err != nil {
		h.logger.Error("場所一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if places == nil {
		places = []location.Place{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": places})
}

// CreatePlace handles POST /api/location/places.
func (h *LocationHandler) CreatePlace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string  `json:"name"`
		Lat     float64 `json:"latitude"`
		Lon     float64 `json:"longitude"`
		RadiusM float64 `json:"radius_m"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	if body.RadiusM <= 0 {
		body.RadiusM = 50
	}

	p := location.Place{
		Name:      body.Name,
		Latitude:  body.Lat,
		Longitude: body.Lon,
		RadiusM:   body.RadiusM,
	}
	store := location.NewStore(h.db)
	if err := store.CreatePlace(r.Context(), p); err != nil {
		h.logger.Error("場所の作成に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	h.notifyAgentReload(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// UpdatePlace handles PUT /api/location/places/{id}.
func (h *LocationHandler) UpdatePlace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}

	var body struct {
		Name    string  `json:"name"`
		Lat     float64 `json:"latitude"`
		Lon     float64 `json:"longitude"`
		RadiusM float64 `json:"radius_m"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	p := location.Place{
		ID:        id,
		Name:      body.Name,
		Latitude:  body.Lat,
		Longitude: body.Lon,
		RadiusM:   body.RadiusM,
	}
	store := location.NewStore(h.db)
	if err := store.UpdatePlace(r.Context(), p); err != nil {
		h.logger.Error("場所の更新に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	h.notifyAgentReload(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeletePlace handles DELETE /api/location/places/{id}.
func (h *LocationHandler) DeletePlace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}

	store := location.NewStore(h.db)
	if err := store.DeletePlace(r.Context(), id); err != nil {
		h.logger.Error("場所の削除に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	h.notifyAgentReload(r)
	w.WriteHeader(http.StatusNoContent)
}
