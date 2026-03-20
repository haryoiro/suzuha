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
		a.logConversationTurn(ctx, agentCtx, p.TurnStartIdx, p.Channel)
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

// doCompactWith performs the actual compaction logic for the given context and source key.
// If async is true, new messages appended during compaction are preserved via CompactReplace;
// otherwise the context is fully replaced via ReplaceAll.
func (a *Agent) doCompactWith(ctx context.Context, agentCtx *Context, sourceKey SourceKey, msgs []llm.Message, async bool) {
	n := len(msgs)
	target := n / 2

	if a.consol != nil {
		result, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
			Messages:    msgs,
			TargetCount: target,
		})
		if err != nil {
			a.logger.Warn("記憶の整理がうまくいかず、古い方から忘れた", "error", err)
			agentCtx.TruncateOldest(n / 2)
			a.resetAndPersistWith(ctx, agentCtx, sourceKey)
			return
		}

		if len(result.KeepIndices) == 0 {
			a.logger.Warn("整理する記憶の選別ができず、古い方から忘れた")
			agentCtx.TruncateOldest(n / 2)
			a.resetAndPersistWith(ctx, agentCtx, sourceKey)
			return
		}

		var kept []llm.Message
		for _, idx := range result.KeepIndices {
			if idx >= 0 && idx < n {
				kept = append(kept, msgs[idx])
			}
		}
		if async {
			agentCtx.CompactReplace(n, kept)
		} else {
			agentCtx.ReplaceAll(kept)
		}
		a.resetAndPersistWith(ctx, agentCtx, sourceKey)
		a.logger.Info("記憶を整理した", "kept", len(kept), "original", n)
		return
	}

	// No consolidator available — simple truncation fallback.
	agentCtx.TruncateOldest(n / 2)
	a.resetAndPersistWith(ctx, agentCtx, sourceKey)
	a.logger.Info("古い記憶を忘れた", "original", n)
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

// logConversationTurn logs all messages added during the current turn
// to the conversation_logs table for fine-tuning data collection.
func (a *Agent) logConversationTurn(ctx context.Context, agentCtx *Context, startIdx int, channel string) {
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
			`INSERT INTO conversation_logs (turn_id, channel_id, role, content, user_id, user_name, message_id, tool_calls, tool_call_id, timestamp)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			turnID, channel, msg.Role, msg.Content,
			nullIfEmpty(msg.UserID), nullIfEmpty(msg.UserName), nullIfEmpty(msg.MessageID),
			toolCallsJSON, toolCallID,
			msg.Timestamp,
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
