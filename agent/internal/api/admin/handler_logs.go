package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

func (h *AdminHandler) ConversationLogsList(ctx context.Context) (*gen.ConversationLogsListOK, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT channel_id, COUNT(*) as count, COUNT(DISTINCT turn_id) as turns,
		        MIN(timestamp) as first, MAX(timestamp) as last
		 FROM conversation_logs
		 GROUP BY channel_id
		 ORDER BY last DESC`)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	var data []gen.ConversationSummary
	for rows.Next() {
		var s gen.ConversationSummary
		if err := rows.Scan(&s.ChannelID, &s.Messages, &s.Turns, &s.First, &s.Last); err != nil {
			continue
		}
		data = append(data, s)
	}
	if data == nil {
		data = []gen.ConversationSummary{}
	}
	return &gen.ConversationLogsListOK{Data: data}, nil
}

func (h *AdminHandler) ConversationLogsExport(ctx context.Context, params gen.ConversationLogsExportParams) (gen.ConversationLogsExportOK, error) {
	channelID := params.ChannelID.Or("")
	since := params.Since.Or("")
	until := params.Until.Or("")

	query := `SELECT turn_id, role, content, tool_calls, tool_call_id
		FROM conversation_logs WHERE 1=1`
	var args []any
	argIdx := 1

	if channelID != "" {
		query += fmt.Sprintf(` AND channel_id = $%d`, argIdx)
		args = append(args, channelID)
		argIdx++
	}
	if since != "" {
		query += fmt.Sprintf(` AND timestamp >= $%d`, argIdx)
		args = append(args, since)
		argIdx++
	}
	if until != "" {
		query += fmt.Sprintf(` AND timestamp <= $%d`, argIdx)
		args = append(args, until)
		argIdx++
	}
	query += ` ORDER BY id ASC`

	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return gen.ConversationLogsExportOK{}, fmt.Errorf("internal error")
	}
	defer rows.Close()

	type ftMessage struct {
		Role       string          `json:"role"`
		Content    string          `json:"content,omitempty"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	type turn struct {
		messages []ftMessage
	}
	var turns []turn
	turnMap := map[string]int{}

	for rows.Next() {
		var turnID, role, content string
		var toolCalls, toolCallID *string
		if err := rows.Scan(&turnID, &role, &content, &toolCalls, &toolCallID); err != nil {
			continue
		}

		msg := ftMessage{Role: role, Content: content}
		if toolCalls != nil {
			msg.ToolCalls = json.RawMessage(*toolCalls)
		}
		if toolCallID != nil {
			msg.ToolCallID = *toolCallID
		}

		idx, ok := turnMap[turnID]
		if !ok {
			idx = len(turns)
			turnMap[turnID] = idx
			turns = append(turns, turn{})
		}
		turns[idx].messages = append(turns[idx].messages, msg)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, t := range turns {
		enc.Encode(map[string]any{"messages": t.messages})
	}

	return gen.ConversationLogsExportOK{Data: &buf}, nil
}
