package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
)

// ConversationLogsHandler provides HTTP handlers for conversation log export.
type ConversationLogsHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewConversationLogsHandler creates a new ConversationLogsHandler.
func NewConversationLogsHandler(db *sql.DB, logger *slog.Logger) *ConversationLogsHandler {
	return &ConversationLogsHandler{db: db, logger: logger}
}

type convLogRow struct {
	TurnID      string
	Role        string
	Content     string
	ToolCalls   *string
	ToolCallID  *string
}

type ftMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Export handles GET /api/conversation-logs/export.
// Query params:
//   - channel_id: filter by channel (optional)
//   - since: ISO8601 datetime lower bound (optional)
//   - until: ISO8601 datetime upper bound (optional)
//
// Returns JSONL in OpenAI fine-tuning format.
func (h *ConversationLogsHandler) Export(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	channelID := q.Get("channel_id")
	since := q.Get("since")
	until := q.Get("until")

	query := `SELECT turn_id, role, content, tool_calls, tool_call_id
		FROM conversation_logs WHERE 1=1`
	var args []any

	if channelID != "" {
		query += ` AND channel_id = ?`
		args = append(args, channelID)
	}
	if since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, since)
	}
	if until != "" {
		query += ` AND timestamp <= ?`
		args = append(args, until)
	}
	query += ` ORDER BY id ASC`

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("会話ログのエクスポートに失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Group rows by turn_id.
	type turn struct {
		messages []ftMessage
	}
	var turns []turn
	turnMap := map[string]int{} // turn_id → index in turns

	for rows.Next() {
		var row convLogRow
		if err := rows.Scan(&row.TurnID, &row.Role, &row.Content, &row.ToolCalls, &row.ToolCallID); err != nil {
			h.logger.Error("会話ログ行のスキャンに失敗", "error", err)
			continue
		}

		msg := ftMessage{
			Role:    row.Role,
			Content: row.Content,
		}
		if row.ToolCalls != nil {
			msg.ToolCalls = json.RawMessage(*row.ToolCalls)
		}
		if row.ToolCallID != nil {
			msg.ToolCallID = *row.ToolCallID
		}

		idx, ok := turnMap[row.TurnID]
		if !ok {
			idx = len(turns)
			turnMap[row.TurnID] = idx
			turns = append(turns, turn{})
		}
		turns[idx].messages = append(turns[idx].messages, msg)
	}

	// Stream JSONL response.
	w.Header().Set("Content-Type", "application/jsonl")
	w.Header().Set("Content-Disposition", `attachment; filename="conversation_logs.jsonl"`)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, t := range turns {
		if err := enc.Encode(map[string]any{"messages": t.messages}); err != nil {
			h.logger.Error("会話ターンのエンコードに失敗", "error", err)
			return
		}
	}
}

// List handles GET /api/conversation-logs.
// Returns conversation log stats grouped by channel.
func (h *ConversationLogsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT channel_id, COUNT(*) as count, COUNT(DISTINCT turn_id) as turns,
		        MIN(timestamp) as first, MAX(timestamp) as last
		 FROM conversation_logs
		 GROUP BY channel_id
		 ORDER BY last DESC`)
	if err != nil {
		h.logger.Error("会話ログ一覧の取得に失敗", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type channelStats struct {
		ChannelID string `json:"channel_id"`
		Messages  int    `json:"messages"`
		Turns     int    `json:"turns"`
		First     string `json:"first"`
		Last      string `json:"last"`
	}
	var stats []channelStats
	for rows.Next() {
		var s channelStats
		if err := rows.Scan(&s.ChannelID, &s.Messages, &s.Turns, &s.First, &s.Last); err != nil {
			continue
		}
		stats = append(stats, s)
	}
	if stats == nil {
		stats = []channelStats{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": stats})
}
