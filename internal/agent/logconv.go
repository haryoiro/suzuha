package agent

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// logConversationTurn logs all messages added during the current turn
// (from startIdx onwards) to the conversation_logs table for fine-tuning
// data collection. System messages are excluded.
func (a *Agent) logConversationTurn(ctx context.Context, startIdx int, channel string) {
	if a.db == nil {
		return
	}

	msgs := a.ctx.Messages()
	if startIdx >= len(msgs) {
		return
	}

	turnID := uuid.NewString()

	for _, msg := range msgs[startIdx:] {
		if msg.Role == "system" {
			continue
		}

		var toolCallsJSON *string
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err == nil {
				s := string(b)
				toolCallsJSON = &s
			}
		}

		var toolCallID *string
		if msg.ToolCallID != "" {
			toolCallID = &msg.ToolCallID
		}

		_, err := a.db.ExecContext(ctx,
			`INSERT INTO conversation_logs (turn_id, channel_id, role, content, user_id, user_name, message_id, tool_calls, tool_call_id, timestamp)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			turnID, channel, msg.Role, msg.Content,
			nullIfEmpty(msg.UserID), nullIfEmpty(msg.UserName), nullIfEmpty(msg.MessageID),
			toolCallsJSON, toolCallID,
			msg.Timestamp,
		)
		if err != nil {
			a.logger.Warn("log conversation: insert failed", "error", err, "role", msg.Role)
		}
	}
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
