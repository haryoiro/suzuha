package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	boredomRate = 8.0
	boredomMax  = 100.0
)

// BoredomHandler serves the current boredom status.
type BoredomHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewBoredomHandler creates a new BoredomHandler.
func NewBoredomHandler(db *sql.DB, logger *slog.Logger) *BoredomHandler {
	return &BoredomHandler{db: db, logger: logger}
}

type boredomResponse struct {
	Boredom         float64 `json:"boredom"`
	LastInteraction *string `json:"last_interaction"`
	LastChannel     string  `json:"last_channel,omitempty"`
	LastPostedAt    *string `json:"last_posted_at"`
	PostThreshold   float64 `json:"post_threshold"`
}

// Get returns the current global boredom status based on the most recent
// user interaction across all channels.
func (h *BoredomHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := boredomResponse{
		PostThreshold: 20.0,
	}

	// Get most recent user interaction across all channels.
	var lastMsg time.Time
	var channelID string
	err := h.db.QueryRowContext(ctx,
		`SELECT channel_id, last_user_message_at
		 FROM channel_activity
		 WHERE last_user_message_at IS NOT NULL
		 ORDER BY last_user_message_at DESC
		 LIMIT 1`,
	).Scan(&channelID, &lastMsg)
	if err == nil {
		s := lastMsg.Format(time.RFC3339)
		resp.LastInteraction = &s

		// Resolve channel name.
		var name string
		if h.db.QueryRowContext(ctx,
			`SELECT name FROM channels WHERE id = ?`, channelID,
		).Scan(&name) == nil && name != "" {
			resp.LastChannel = "#" + name
		} else {
			resp.LastChannel = channelID
		}

		hours := time.Since(lastMsg).Hours()
		if hours < 0 {
			hours = 0
		}
		b := hours * boredomRate
		if b > boredomMax {
			b = boredomMax
		}
		resp.Boredom = b
	} else {
		resp.Boredom = boredomMax
	}

	// Get last posted time from task_state.
	var stateJSON string
	err = h.db.QueryRowContext(ctx,
		`SELECT state FROM task_state WHERE task_name = 'topics'`,
	).Scan(&stateJSON)
	if err == nil {
		var state struct {
			LastPostedAt time.Time `json:"last_posted_at"`
		}
		if json.Unmarshal([]byte(stateJSON), &state) == nil && !state.LastPostedAt.IsZero() {
			s := state.LastPostedAt.Format(time.RFC3339)
			resp.LastPostedAt = &s
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
