package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/consolidator"
	"github.com/haryoiro/suzuha/internal/llm"
)

// Reflect is the backward-compatible wrapper that calls ReflectWith
// with the discord source key.
func (a *Agent) Reflect(ctx context.Context, p *Perception) {
	a.ReflectWith(ctx, a.contexts[SourceKeyDiscord], p, SourceKeyDiscord)
}

// ReflectWith logs the conversation turn, persists context to DB,
// and runs post-response bookkeeping for the given source key.
func (a *Agent) ReflectWith(ctx context.Context, agentCtx *Context, p *Perception, sourceKey SourceKey) {
	if p.Channel != "" {
		a.logConversationTurn(ctx, agentCtx, p.TurnStartIdx, p.Channel, sourceKey)
	}
	persistContextWith(ctx, a.db, agentCtx, a.logger, string(sourceKey))
}

// compactAsync is the backward-compatible wrapper.
func (a *Agent) compactAsync(ctx context.Context) {
	a.compactAsyncFor(ctx, a.contexts[SourceKeyDiscord], SourceKeyDiscord)
}

// compactAsyncFor triggers context compaction in a background goroutine
// for the given source key.
// The pipeline continues processing while compaction runs.
// Only one compaction runs at a time per source key; concurrent requests are skipped.
func (a *Agent) compactAsyncFor(ctx context.Context, agentCtx *Context, sourceKey SourceKey) {
	mu := a.compactMu[sourceKey]
	if !mu.TryLock() {
		a.logger.Debug("記憶の整理はもうやってる", "source_key", string(sourceKey))
		return
	}

	snapshot := agentCtx.Messages()
	snapshotLen := len(snapshot)

	go func() {
		defer mu.Unlock()
		a.logger.Info("裏で記憶を整理し始める", "snapshot_len", snapshotLen, "source_key", string(sourceKey))
		a.doCompactWith(ctx, agentCtx, sourceKey, snapshot, true)
	}()
}

// compact triggers context compaction synchronously (used by ForceCompact).
func (a *Agent) compact(ctx context.Context) {
	agentCtx := a.contexts[SourceKeyDiscord]
	msgs := agentCtx.Messages()
	a.doCompactWith(ctx, agentCtx, SourceKeyDiscord, msgs, false)
}

// doCompactWith extracts long-term memories from the context, then clears all
// messages. The next turn will re-inject recent channel history via
// injectChannelHistoryWith, so no messages need to be kept.
// If async is true, messages appended during compaction are preserved.
func (a *Agent) doCompactWith(ctx context.Context, agentCtx *Context, sourceKey SourceKey, msgs []llm.Message, async bool) {
	n := len(msgs)

	// Extract long-term memories via consolidator (best-effort).
	if a.consol != nil {
		_, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
			Messages: msgs,
		})
		if err != nil {
			a.logger.Warn("記憶の抽出に失敗", "error", err)
		}
	}

	// Clear all messages; channel history is re-injected next turn.
	if async {
		agentCtx.CompactReplace(n, nil)
	} else {
		agentCtx.ReplaceAll(nil)
	}
	a.resetAndPersistWith(ctx, agentCtx, sourceKey)
	a.logger.Info("記憶を整理した（全クリア）", "original", n)
}

// resetAndPersistWith clears injected state and saves context to DB.
func (a *Agent) resetAndPersistWith(ctx context.Context, agentCtx *Context, sourceKey SourceKey) {
	agentCtx.ResetInjectedUsers()
	agentCtx.ResetSeenChannels()
	persistContextWith(ctx, a.db, agentCtx, a.logger, string(sourceKey))
}

// resetAndPersist is the backward-compatible wrapper.
func (a *Agent) resetAndPersist(ctx context.Context) {
	a.resetAndPersistWith(ctx, a.contexts[SourceKeyDiscord], SourceKeyDiscord)
}

// DeleteChannel removes all data for a channel: in-memory context messages,
// channel_settings, channel_activity, conversation_logs, and user_guild_channels.
func (a *Agent) DeleteChannel(ctx context.Context, key SourceKey, channelID string) int {
	actx := a.contexts[key]
	removed := actx.RemoveByChannel(channelID)
	if a.db != nil {
		a.db.ExecContext(ctx, `DELETE FROM channel_settings WHERE channel_id = ?`, channelID)
		a.db.ExecContext(ctx, `DELETE FROM channel_activity WHERE channel_id = ?`, channelID)
		a.db.ExecContext(ctx, `DELETE FROM conversation_logs WHERE channel_id = ?`, channelID)
		a.db.ExecContext(ctx, `DELETE FROM user_guild_channels WHERE channel_id = ?`, channelID)
	}
	if a.channelSettings != nil {
		a.channelSettings.Reload(ctx)
	}
	persistContextWith(ctx, a.db, actx, a.logger, string(key))
	return removed
}

// logConversationTurn logs all messages added during the current turn
// to the conversation_logs table for fine-tuning data collection.
func (a *Agent) logConversationTurn(ctx context.Context, agentCtx *Context, startIdx int, channel string, sourceKey SourceKey) {
	if a.db == nil {
		return
	}

	msgs := agentCtx.Messages()
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
			`INSERT INTO conversation_logs (turn_id, channel_id, role, content, user_id, user_name, message_id, tool_calls, tool_call_id, timestamp, source_key)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			turnID, channel, msg.Role, msg.Content,
			nullIfEmpty(msg.UserID), nullIfEmpty(msg.UserName), nullIfEmpty(msg.MessageID),
			toolCallsJSON, toolCallID,
			msg.Timestamp, string(sourceKey),
		)
		if err != nil {
			a.logger.Warn("会話の記録に失敗", "error", err, "role", msg.Role)
		}
	}
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// persistContext is the backward-compatible wrapper that uses source_key "discord".
func persistContext(ctx context.Context, db *sql.DB, agentCtx *Context, logger *slog.Logger) {
	persistContextWith(ctx, db, agentCtx, logger, string(SourceKeyDiscord))
}

// persistContextWith saves the current context messages to the database
// using the given source key.
func persistContextWith(ctx context.Context, db *sql.DB, agentCtx *Context, logger *slog.Logger, sourceKey string) {
	if db == nil {
		return
	}
	msgs := agentCtx.Messages()
	data, err := json.Marshal(msgs)
	if err != nil {
		logger.Warn("記憶の保存に失敗 (変換)", "error", err)
		return
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR REPLACE INTO context_snapshot (source_key, messages, updated_at) VALUES (?, ?, datetime('now'))`,
		sourceKey, string(data))
	if err != nil {
		logger.Warn("記憶の保存に失敗 (書き込み)", "error", err)
	}
}

// loadContext is the backward-compatible wrapper that loads the discord context.
func loadContext(db *sql.DB, logger *slog.Logger) []llm.Message {
	return loadContextWith(db, logger, string(SourceKeyDiscord))
}

// loadContextWith loads saved context messages from the database for the given source key.
func loadContextWith(db *sql.DB, logger *slog.Logger, sourceKey string) []llm.Message {
	if db == nil {
		return nil
	}
	var data string
	err := db.QueryRow(`SELECT messages FROM context_snapshot WHERE source_key = ?`, sourceKey).Scan(&data)
	if err != nil {
		if err != sql.ErrNoRows {
			logger.Warn("記憶の読み込みに失敗 (DB)", "error", err)
		}
		return nil
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(data), &msgs); err != nil {
		logger.Warn("記憶の読み込みに失敗 (解析)", "error", err)
		return nil
	}
	return msgs
}
