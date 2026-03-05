package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/haryoiro/suzuha/internal/schedule"
	"github.com/robfig/cron/v3"
)

// ActionsHandler provides HTTP handlers for scheduled actions.
type ActionsHandler struct {
	store  *schedule.Store
	logger *slog.Logger
}

// NewActionsHandler creates a new ActionsHandler.
func NewActionsHandler(store *schedule.Store, logger *slog.Logger) *ActionsHandler {
	return &ActionsHandler{store: store, logger: logger}
}

type actionJSON struct {
	ID          string  `json:"id"`
	ChannelID   string  `json:"channel_id"`
	Content     string  `json:"content"`
	Mode        string  `json:"mode"`
	ScheduledAt string  `json:"scheduled_at"`
	CronExpr    *string `json:"cron_expr"`
	CreatedBy   *string `json:"created_by"`
	Status      string  `json:"status"`
	ExecutedAt  *string `json:"executed_at"`
	CreatedAt   string  `json:"created_at"`
}

func actionToJSON(a schedule.Action) actionJSON {
	j := actionJSON{
		ID:          a.ID,
		ChannelID:   a.ChannelID,
		Content:     a.Content,
		Mode:        a.Mode,
		ScheduledAt: a.ScheduledAt.Format(time.RFC3339),
		Status:      a.Status,
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
	}
	if a.CronExpr != "" {
		j.CronExpr = &a.CronExpr
	}
	if a.CreatedBy != "" {
		j.CreatedBy = &a.CreatedBy
	}
	if a.ExecutedAt != nil {
		s := a.ExecutedAt.Format(time.RFC3339)
		j.ExecutedAt = &s
	}
	return j
}

// List handles GET /api/scheduled-actions.
func (h *ActionsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	actions, err := h.store.List(r.Context(), schedule.ActionListOpts{
		Status: q.Get("status"),
		Limit:  limit,
	})
	if err != nil {
		h.logger.Error("アクション一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	result := make([]actionJSON, 0, len(actions))
	for _, a := range actions {
		result = append(result, actionToJSON(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

// Create handles POST /api/scheduled-actions.
func (h *ActionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelID   string  `json:"channel_id"`
		Content     string  `json:"content"`
		Mode        string  `json:"mode"`
		ScheduledAt string  `json:"scheduled_at"`
		CronExpr    *string `json:"cron_expr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" || body.Content == "" {
		http.Error(w, `{"error":"channel_id and content are required"}`, http.StatusBadRequest)
		return
	}

	// Resolve scheduled_at: explicit value, or auto-calculate from cron_expr.
	var scheduledAt time.Time
	switch {
	case body.ScheduledAt != "":
		parsed, parseErr := time.Parse(time.RFC3339, body.ScheduledAt)
		if parseErr != nil {
			http.Error(w, `{"error":"scheduled_at must be RFC3339 format"}`, http.StatusBadRequest)
			return
		}
		scheduledAt = parsed.UTC()
	case body.CronExpr != nil && *body.CronExpr != "":
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, parseErr := parser.Parse(*body.CronExpr)
		if parseErr != nil {
			http.Error(w, `{"error":"invalid cron_expr: `+parseErr.Error()+`"}`, http.StatusBadRequest)
			return
		}
		scheduledAt = sched.Next(time.Now()).UTC()
	default:
		http.Error(w, `{"error":"either scheduled_at or cron_expr is required"}`, http.StatusBadRequest)
		return
	}

	var cronExpr string
	if body.CronExpr != nil {
		cronExpr = *body.CronExpr
	}

	a := &schedule.Action{
		ChannelID:   body.ChannelID,
		Content:     body.Content,
		Mode:        body.Mode,
		ScheduledAt: scheduledAt,
		CronExpr:    cronExpr,
		CreatedBy:   "admin",
	}
	if err := h.store.Create(r.Context(), a); err != nil {
		h.logger.Error("アクションの作成に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": a.ID}})
}

// Update handles PUT /api/scheduled-actions/{id}.
func (h *ActionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		ChannelID   *string `json:"channel_id"`
		Content     *string `json:"content"`
		Mode        *string `json:"mode"`
		ScheduledAt *string `json:"scheduled_at"`
		CronExpr    *string `json:"cron_expr"`
		Status      *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	fields := schedule.ActionUpdateFields{
		ChannelID:   body.ChannelID,
		Content:     body.Content,
		Mode:        body.Mode,
		ScheduledAt: body.ScheduledAt,
		CronExpr:    body.CronExpr,
		Status:      body.Status,
	}

	if err := h.store.Update(r.Context(), id, fields); err != nil {
		h.logger.Error("アクションの更新に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/scheduled-actions/{id}.
func (h *ActionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.logger.Error("アクションの削除に失敗", "error", err)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
