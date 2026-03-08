package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

func (h *AdminHandler) BoredomGet(ctx context.Context) (*api.BoredomStatus, error) {
	const (
		boredomRate = 8.0
		boredomMax  = 100.0
	)

	resp := &api.BoredomStatus{
		PostThreshold: 20.0,
		Boredom:       boredomMax,
	}

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
		resp.LastInteraction = api.NewOptString(lastMsg.Format(time.RFC3339))

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
	}

	var stateJSON string
	err = h.db.QueryRowContext(ctx,
		`SELECT state FROM task_state WHERE task_name = 'topics'`,
	).Scan(&stateJSON)
	if err == nil {
		var state struct {
			LastPostedAt time.Time `json:"last_posted_at"`
		}
		if json.Unmarshal([]byte(stateJSON), &state) == nil && !state.LastPostedAt.IsZero() {
			resp.LastPostedAt = api.NewOptString(state.LastPostedAt.Format(time.RFC3339))
		}
	}

	return resp, nil
}
