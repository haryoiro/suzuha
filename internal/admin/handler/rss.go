package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/haryoiro/suzuha/internal/rss"
)

// RSSHandler provides HTTP handlers for RSS feed management.
type RSSHandler struct {
	store  *rss.FeedStore
	logger *slog.Logger
}

// NewRSSHandler creates a new RSSHandler.
func NewRSSHandler(db *sql.DB, logger *slog.Logger) *RSSHandler {
	return &RSSHandler{store: rss.NewFeedStore(db), logger: logger}
}

// List handles GET /api/feeds.
func (h *RSSHandler) List(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.store.ListAll(r.Context())
	if err != nil {
		h.logger.Error("list feeds", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if feeds == nil {
		feeds = []rss.Feed{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": feeds, "total": len(feeds)})
}

type createFeedRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	ChannelID string `json:"channel_id"`
}

// Create handles POST /api/feeds.
func (h *RSSHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.URL == "" || req.ChannelID == "" {
		http.Error(w, `{"error":"name, url, and channel_id are required"}`, http.StatusBadRequest)
		return
	}

	feed := &rss.Feed{
		Name:      req.Name,
		URL:       req.URL,
		ChannelID: req.ChannelID,
		Enabled:   true,
	}
	if err := h.store.AddFeed(r.Context(), feed); err != nil {
		h.logger.Error("create feed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"data": feed})
}

// Get handles GET /api/feeds/{id}.
func (h *RSSHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	feed, err := h.store.GetFeed(r.Context(), id)
	if err != nil {
		h.logger.Error("get feed", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": feed})
}

type updateFeedRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	ChannelID string `json:"channel_id"`
	Enabled   *bool  `json:"enabled"`
}

// Update handles PUT /api/feeds/{id}.
func (h *RSSHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	feed, err := h.store.GetFeed(r.Context(), id)
	if err != nil {
		h.logger.Error("get feed for update", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	if req.Name != "" {
		feed.Name = req.Name
	}
	if req.URL != "" {
		feed.URL = req.URL
	}
	if req.ChannelID != "" {
		feed.ChannelID = req.ChannelID
	}
	if req.Enabled != nil {
		feed.Enabled = *req.Enabled
	}

	if err := h.store.UpdateFeed(r.Context(), feed); err != nil {
		h.logger.Error("update feed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": feed})
}

// Delete handles DELETE /api/feeds/{id}.
func (h *RSSHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.RemoveFeed(r.Context(), id); err != nil {
		h.logger.Error("delete feed", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListItems handles GET /api/feeds/{id}/items.
func (h *RSSHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 20
	}

	items, total, err := h.store.ListItems(r.Context(), id, offset, limit)
	if err != nil {
		h.logger.Error("list feed items", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []rss.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "total": total})
}

// Stats handles GET /api/feeds/stats.
func (h *RSSHandler) Stats(w http.ResponseWriter, r *http.Request) {
	total, enabled, err := h.store.CountFeeds(r.Context())
	if err != nil {
		h.logger.Error("feed stats", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":   total,
		"enabled": enabled,
	})
}
