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

// Reflect logs the conversation turn, persists context to DB,
// and runs post-response bookkeeping.
func (a *Agent) Reflect(ctx context.Context, p *Perception) {
	if p.Channel != "" {
		a.logConversationTurn(ctx, p.TurnStartIdx, p.Channel)
	}
	persistContext(ctx, a.db, a.ctx, a.logger)
}

// compactAsync triggers context compaction in a background goroutine.
// The pipeline continues processing while compaction runs.
// Only one compaction runs at a time; concurrent requests are skipped.
func (a *Agent) compactAsync(ctx context.Context) {
	if !a.compactMu.TryLock() {
		a.logger.Debug("コンパクション既に実行中、スキップ")
		return
	}

	snapshot := a.ctx.Messages()
	snapshotLen := len(snapshot)
	target := snapshotLen / 2

	go func() {
		defer a.compactMu.Unlock()
		a.logger.Info("バックグラウンドコンパクション開始", "snapshot_len", snapshotLen)

		if a.consol != nil {
			result, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
				Messages:    snapshot,
				TargetCount: target,
			})
			if err != nil {
				a.logger.Warn("コンソリデータの圧縮失敗、切り詰めにフォールバック", "error", err)
				a.ctx.TruncateOldest(snapshotLen / 2)
				a.ctx.ResetInjectedUsers()
				a.ctx.ResetSeenChannels()
				persistContext(ctx, a.db, a.ctx, a.logger)
				return
			}

			if len(result.KeepIndices) == 0 {
				a.logger.Warn("コンソリデータが保持インデックスを返さず、切り詰めにフォールバック")
				a.ctx.TruncateOldest(snapshotLen / 2)
				a.ctx.ResetInjectedUsers()
				a.ctx.ResetSeenChannels()
				persistContext(ctx, a.db, a.ctx, a.logger)
	
				return
			}

			var kept []llm.Message
			for _, idx := range result.KeepIndices {
				if idx >= 0 && idx < len(snapshot) {
					kept = append(kept, snapshot[idx])
				}
			}
			a.ctx.CompactReplace(snapshotLen, kept)
			a.ctx.ResetInjectedUsers()
			a.ctx.ResetSeenChannels()
			persistContext(ctx, a.db, a.ctx, a.logger)


			a.logger.Info("バックグラウンドコンパクション完了",
				"kept", len(kept), "original", snapshotLen)
			return
		}

		// No consolidator available — simple truncation fallback.
		a.ctx.TruncateOldest(snapshotLen / 2)
		a.ctx.ResetInjectedUsers()
		a.ctx.ResetSeenChannels()
		persistContext(ctx, a.db, a.ctx, a.logger)
		a.logger.Info("バックグラウンド切り詰め完了")
	}()
}

// compact triggers context compaction synchronously (used by ForceCompact).
func (a *Agent) compact(ctx context.Context) {
	msgs := a.ctx.Messages()
	target := len(msgs) / 2

	if a.consol != nil {
		result, err := a.consol.Compact(ctx, &consolidator.CompactRequest{
			Messages:    msgs,
			TargetCount: target,
		})
		if err != nil {
			a.logger.Warn("コンソリデータの圧縮失敗、切り詰めにフォールバック", "error", err)
			a.ctx.TruncateOldest(len(msgs) / 2)
			a.ctx.ResetInjectedUsers()
			a.ctx.ResetSeenChannels()
			persistContext(ctx, a.db, a.ctx, a.logger)
			return
		}

		if len(result.KeepIndices) == 0 {
			a.logger.Warn("コンソリデータが保持インデックスを返さず、切り詰めにフォールバック")
			a.ctx.TruncateOldest(len(msgs) / 2)
			a.ctx.ResetInjectedUsers()
			a.ctx.ResetSeenChannels()
			persistContext(ctx, a.db, a.ctx, a.logger)
	
			return
		}

		var kept []llm.Message
		for _, idx := range result.KeepIndices {
			if idx >= 0 && idx < len(msgs) {
				kept = append(kept, msgs[idx])
			}
		}
		a.ctx.ReplaceAll(kept)
		a.ctx.ResetInjectedUsers()
		a.ctx.ResetSeenChannels()
		persistContext(ctx, a.db, a.ctx, a.logger)


		return
	}

	// No consolidator available — simple truncation fallback.
	a.ctx.TruncateOldest(len(msgs) / 2)
	a.ctx.ResetInjectedUsers()
	a.ctx.ResetSeenChannels()
	persistContext(ctx, a.db, a.ctx, a.logger)
}

// logConversationTurn logs all messages added during the current turn
// to the conversation_logs table for fine-tuning data collection.
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
			a.logger.Warn("会話ログ: 挿入失敗", "error", err, "role", msg.Role)
		}
	}
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// persistContext saves the current context messages to the database.
func persistContext(ctx context.Context, db *sql.DB, agentCtx *Context, logger *slog.Logger) {
	if db == nil {
		return
	}
	msgs := agentCtx.Messages()
	data, err := json.Marshal(msgs)
	if err != nil {
		logger.Warn("コンテキスト永続化: マーシャル失敗", "error", err)
		return
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR REPLACE INTO context_snapshot (id, messages, updated_at) VALUES (1, ?, datetime('now'))`,
		string(data))
	if err != nil {
		logger.Warn("コンテキスト永続化: 書き込み失敗", "error", err)
	}
}

// loadContext loads saved context messages from the database.
func loadContext(db *sql.DB, logger *slog.Logger) []llm.Message {
	if db == nil {
		return nil
	}
	var data string
	err := db.QueryRow(`SELECT messages FROM context_snapshot WHERE id = 1`).Scan(&data)
	if err != nil {
		if err != sql.ErrNoRows {
			logger.Warn("コンテキスト読み込み: クエリ失敗", "error", err)
		}
		return nil
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(data), &msgs); err != nil {
		logger.Warn("コンテキスト読み込み: アンマーシャル失敗", "error", err)
		return nil
	}
	return msgs
}
