package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// ActionsHandler provides HTTP handlers for scheduled actions.
type ActionsHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewActionsHandler creates a new ActionsHandler.
func NewActionsHandler(db *sql.DB, logger *slog.Logger) *ActionsHandler {
	return &ActionsHandler{db: db, logger: logger}
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

// List handles GET /api/scheduled-actions.
func (h *ActionsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	status := q.Get("status")

	query := `SELECT id, channel_id, content, COALESCE(mode,'direct'), scheduled_at, cron_expr, created_by, status, executed_at, created_at
	          FROM scheduled_actions`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY scheduled_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("list actions", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	actions := h.scanActions(rows)
	writeJSON(w, http.StatusOK, map[string]any{"data": actions})
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
	if body.ScheduledAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, body.ScheduledAt)
		if parseErr != nil {
			http.Error(w, `{"error":"scheduled_at must be RFC3339 format"}`, http.StatusBadRequest)
			return
		}
		scheduledAt = parsed.UTC()
	} else if body.CronExpr != nil && *body.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, parseErr := parser.Parse(*body.CronExpr)
		if parseErr != nil {
			http.Error(w, `{"error":"invalid cron_expr: `+parseErr.Error()+`"}`, http.StatusBadRequest)
			return
		}
		scheduledAt = sched.Next(time.Now()).UTC()
	} else {
		http.Error(w, `{"error":"either scheduled_at or cron_expr is required"}`, http.StatusBadRequest)
		return
	}

	id := uuid.NewString()
	var cronExpr any
	if body.CronExpr != nil {
		cronExpr = *body.CronExpr
	}

	mode := body.Mode
	if mode == "" {
		mode = "direct"
	}

	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO scheduled_actions (id, channel_id, content, mode, scheduled_at, cron_expr, created_by, status)
		 VALUES (?, ?, ?, ?, ?, ?, 'admin', 'pending')`,
		id, body.ChannelID, body.Content, mode, scheduledAt.Format(time.RFC3339), cronExpr)
	if err != nil {
		h.logger.Error("create action", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
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

	var sets []string
	var args []any
	if body.ChannelID != nil {
		sets = append(sets, "channel_id = ?")
		args = append(args, *body.ChannelID)
	}
	if body.Content != nil {
		sets = append(sets, "content = ?")
		args = append(args, *body.Content)
	}
	if body.Mode != nil {
		sets = append(sets, "mode = ?")
		args = append(args, *body.Mode)
	}
	if body.ScheduledAt != nil {
		sets = append(sets, "scheduled_at = ?")
		args = append(args, *body.ScheduledAt)
	}
	if body.CronExpr != nil {
		sets = append(sets, "cron_expr = ?")
		args = append(args, *body.CronExpr)
	}
	if body.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *body.Status)
	}
	if len(sets) == 0 {
		http.Error(w, `{"error":"no fields to update"}`, http.StatusBadRequest)
		return
	}
	args = append(args, id)

	query := "UPDATE scheduled_actions SET "
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += " WHERE id = ?"

	res, err := h.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("update action", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete handles DELETE /api/scheduled-actions/{id}.
func (h *ActionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM scheduled_actions WHERE id = ?`, id)
	if err != nil {
		h.logger.Error("delete action", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *ActionsHandler) scanActions(rows *sql.Rows) []actionJSON {
	var actions []actionJSON
	for rows.Next() {
		var a actionJSON
		var cronExpr, createdBy, executedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.Content, &a.Mode, &a.ScheduledAt,
			&cronExpr, &createdBy, &a.Status, &executedAt, &a.CreatedAt); err != nil {
			continue
		}
		if cronExpr.Valid {
			a.CronExpr = &cronExpr.String
		}
		if createdBy.Valid {
			a.CreatedBy = &createdBy.String
		}
		if executedAt.Valid {
			a.ExecutedAt = &executedAt.String
		}
		actions = append(actions, a)
	}
	if actions == nil {
		actions = []actionJSON{}
	}
	return actions
}
