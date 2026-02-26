package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/llm"
)

// persistContext saves the current context messages to the database.
// Called after each event is fully processed and after compaction.
func persistContext(ctx context.Context, db *sql.DB, agentCtx *Context, logger *slog.Logger) {
	if db == nil {
		return
	}
	msgs := agentCtx.Messages()
	data, err := json.Marshal(msgs)
	if err != nil {
		logger.Warn("persist context: marshal", "error", err)
		return
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR REPLACE INTO context_snapshot (id, messages, updated_at) VALUES (1, ?, datetime('now'))`,
		string(data))
	if err != nil {
		logger.Warn("persist context: write", "error", err)
	}
}

// loadContext loads saved context messages from the database.
// Returns nil if no saved state exists or on error.
func loadContext(db *sql.DB, logger *slog.Logger) []llm.Message {
	if db == nil {
		return nil
	}
	var data string
	err := db.QueryRow(`SELECT messages FROM context_snapshot WHERE id = 1`).Scan(&data)
	if err != nil {
		if err != sql.ErrNoRows {
			logger.Warn("load context: query", "error", err)
		}
		return nil
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(data), &msgs); err != nil {
		logger.Warn("load context: unmarshal", "error", err)
		return nil
	}
	return msgs
}
