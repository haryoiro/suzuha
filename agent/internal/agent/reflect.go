package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/conversation"
	"github.com/haryoiro/suzuha/internal/llm"
	acq "github.com/haryoiro/suzuha/internal/capability/memory/acquire"
)

// filterOutInjectedHistory は Compact に渡すメッセージから
// injectChannelHistoryWith で注入されたチャンネル履歴を除外する。
// これにより Compact → クリア → 再注入 の重複抽出ループを防ぐ。
// 新形式 (Injected flag) と旧形式 (system prefix) の両対応で、旧 snapshot
// の自然消滅を待つ。
func filterOutInjectedHistory(msgs []llm.Message) []llm.Message {
	filtered := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Injected {
			continue
		}
		if m.Role == "system" && (strings.HasPrefix(m.Content, "[Recent history for channel=") ||
			strings.HasPrefix(m.Content, "[Recent related memories for channel=")) {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

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
	a.persistContext(ctx, agentCtx, sourceKey)
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

	// Extract long-term memories via acquirer (best-effort).
	// 注入されたチャンネル履歴は除外する（再注入で重複抽出されるのを防ぐ）。
	if a.acquirer != nil {
		filtered := filterOutInjectedHistory(msgs)
		_, err := a.acquirer.Acquire(ctx, &acq.AcquireRequest{
			Messages: filtered,
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
	agentCtx.ResetTokenTracking()
	a.persistContext(ctx, agentCtx, sourceKey)
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
	if a.convStore != nil {
		if err := a.convStore.DeleteChannel(ctx, channelID); err != nil {
			a.logger.Warn("チャンネルデータの削除に失敗", "error", err)
		}
	}
	if a.channelSettings != nil {
		a.channelSettings.Reload(ctx)
	}
	a.persistContext(ctx, actx, key)
	return removed
}

// logConversationTurn logs all messages added during the current turn
// to the conversation_logs table for fine-tuning data collection.
func (a *Agent) logConversationTurn(ctx context.Context, agentCtx *Context, startIdx int, channel string, sourceKey SourceKey) {
	if a.convStore == nil {
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
		if msg.Injected {
			continue
		}

		var toolCallsStr string
		if len(msg.ToolCalls) > 0 {
			if b, err := json.Marshal(msg.ToolCalls); err == nil {
				toolCallsStr = string(b)
			}
		}

		err := a.convStore.LogTurn(ctx, conversation.TurnEntry{
			TurnID:     turnID,
			ChannelID:  channel,
			Role:       msg.Role,
			Content:    msg.Content,
			UserID:     msg.UserID,
			UserName:   msg.UserName,
			MessageID:  msg.MessageID,
			ToolCalls:  toolCallsStr,
			ToolCallID: msg.ToolCallID,
			SourceKey:  string(sourceKey),
			Timestamp:  msg.Timestamp,
		})
		if err != nil {
			a.logger.Warn("会話の記録に失敗", "error", err, "role", msg.Role)
		}
	}
}

// persistContext は convStore 経由でコンテキストを保存する。
func (a *Agent) persistContext(ctx context.Context, agentCtx *Context, sourceKey SourceKey) {
	if a.convStore == nil {
		return
	}
	if err := a.convStore.SaveSnapshot(ctx, string(sourceKey), agentCtx.PersistableMessages()); err != nil {
		a.logger.Warn("記憶の保存に失敗", "error", err)
	}
}
