package agent

import (
	"sync"

	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
)

// Context manages the short-term message history (in-memory).
// The system prompt (IDENTITY.md) is stored separately from messages
// so it is never affected by compaction or truncation.
type Context struct {
	mu            sync.RWMutex
	messages      []llm.Message
	systemPrompt  string          // pinned system prompt, immune to compaction
	maxTokens     int
	injectedUsers map[string]bool // tracks which user IDs have had profiles injected
	seenChannels  map[string]bool // tracks channels with bootstrapped history

	// tokenCounter はプロバイダ固有のトークンカウンタ。
	// nil の場合は textutil.EstimateTokens にフォールバック。
	tokenCounter func(string) int

	// Token tracking: プロバイダの実測値をベースに差分推定で補完する。
	// lastActualTokens はプロバイダが返した PromptTokens（bg/fg/tools 全込み）。
	// estimateAtSnapshot は lastActualTokens 取得時点の EstimatedTokens。
	// 差分 = 現在の推定 - スナップショット時推定 でメッセージ増分を反映する。
	lastActualTokens   int
	estimateAtSnapshot int
}

// NewContext creates a context manager.
func NewContext(maxTokens int) *Context {
	return &Context{
		maxTokens:     maxTokens,
		injectedUsers: make(map[string]bool),
		seenChannels:  make(map[string]bool),
	}
}

// SetSystemPrompt sets the pinned system prompt (never compacted).
func (c *Context) SetSystemPrompt(prompt string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = prompt
}

// SystemPrompt returns the pinned system prompt.
func (c *Context) SystemPrompt() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.systemPrompt
}

// MessagesWithSystem returns the system prompt (as the first message)
// followed by all conversation messages. Used when calling the LLM.
func (c *Context) MessagesWithSystem() []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]llm.Message, 0, 1+len(c.messages))
	if c.systemPrompt != "" {
		out = append(out, llm.Message{
			Role:    "system",
			Content: c.systemPrompt,
		})
	}
	out = append(out, c.messages...)
	return out
}

// Add appends a message to the context.
func (c *Context) Add(msg llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg)
}

// Messages returns a copy of all messages.
func (c *Context) Messages() []llm.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]llm.Message, len(c.messages))
	copy(out, c.messages)
	return out
}

// Len returns the message count.
func (c *Context) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.messages)
}

// SetTokenCounter はプロバイダ固有のトークンカウンタを設定する。
func (c *Context) SetTokenCounter(fn func(string) int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokenCounter = fn
}

// countTokens は設定されたカウンタでトークン数を返す（ロック不要）。
func (c *Context) countTokens(text string) int {
	if c.tokenCounter != nil {
		return c.tokenCounter(text)
	}
	return textutil.EstimateTokens(text)
}

// estimatedTokensLocked はロックを取らずに推定トークン数を返す（内部用）。
func (c *Context) estimatedTokensLocked() int {
	total := 0
	if c.systemPrompt != "" {
		total += c.countTokens(c.systemPrompt) + 4
	}
	for _, m := range c.messages {
		total += c.countTokens(m.Content) + 4
	}
	return total
}

// EstimatedTokens returns a rough token count using Unicode-aware heuristics.
// Includes the pinned system prompt.
func (c *Context) EstimatedTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.estimatedTokensLocked()
}

// UsageRatio はプロバイダの実測トークン数をベースに使用率を返す。
// 実測値がない場合（初回応答前）はヒューリスティック推定にフォールバック。
func (c *Context) UsageRatio() float64 {
	if c.maxTokens <= 0 {
		return 0
	}
	c.mu.RLock()
	actual := c.lastActualTokens
	snapshot := c.estimateAtSnapshot
	c.mu.RUnlock()

	if actual > 0 {
		currentEst := c.EstimatedTokens()
		delta := currentEst - snapshot
		if delta < 0 {
			delta = 0
		}
		return float64(actual+delta) / float64(c.maxTokens)
	}
	return float64(c.EstimatedTokens()) / float64(c.maxTokens)
}

// CalibrateTokens はプロバイダが返した PromptTokens（実測値）を記録する。
// bg/fg/tools/directive を含む全トークン数なので、compact 判定の精度が上がる。
func (c *Context) CalibrateTokens(actualPromptTokens int) {
	if actualPromptTokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastActualTokens = actualPromptTokens
	c.estimateAtSnapshot = c.estimatedTokensLocked()
}

// TokenCalibration はプロバイダ実測値と推定値の比率を返す（ログ用）。
func (c *Context) TokenCalibration() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	est := c.estimatedTokensLocked()
	if est <= 0 || c.lastActualTokens <= 0 {
		return 0
	}
	return float64(c.lastActualTokens) / float64(est)
}

// ActualTokens はプロバイダ実測値ベースのトークン数を返す。
// 実測値がない場合は EstimatedTokens にフォールバック。
func (c *Context) ActualTokens() int {
	c.mu.RLock()
	actual := c.lastActualTokens
	snapshot := c.estimateAtSnapshot
	c.mu.RUnlock()

	if actual > 0 {
		currentEst := c.EstimatedTokens()
		delta := currentEst - snapshot
		if delta < 0 {
			delta = 0
		}
		return actual + delta
	}
	return c.EstimatedTokens()
}

// ResetTokenTracking は compact 後にトークン追跡をリセットする。
func (c *Context) ResetTokenTracking() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastActualTokens = 0
	c.estimateAtSnapshot = 0
}

// KeepOnly retains only the messages at the given indices.
func (c *Context) KeepOnly(indices []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	kept := make([]llm.Message, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < len(c.messages) {
			kept = append(kept, c.messages[idx])
		}
	}
	c.messages = kept
}

// UpdateUserName replaces UserName in all messages matching the given UserID.
// Used after a nickname change to keep short-term memory consistent.
func (c *Context) UpdateUserName(userID, newName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.messages {
		if c.messages[i].UserID == userID {
			c.messages[i].UserName = newName
		}
	}
}

// HasUserProfile returns true if the given user's profile has been injected.
func (c *Context) HasUserProfile(userID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.injectedUsers[userID]
}

// MarkUserProfileInjected records that a user's profile has been injected.
func (c *Context) MarkUserProfileInjected(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.injectedUsers[userID] = true
}

// PopLast removes and returns the last message. Returns false if empty.
func (c *Context) PopLast() (llm.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return llm.Message{}, false
	}
	last := c.messages[len(c.messages)-1]
	c.messages = c.messages[:len(c.messages)-1]
	return last, true
}

// ResetInjectedUsers clears the user profile tracking (called after compaction).
func (c *Context) ResetInjectedUsers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.injectedUsers = make(map[string]bool)
}

// HasChannelHistory returns true if the given channel has been bootstrapped.
func (c *Context) HasChannelHistory(channelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.seenChannels[channelID]
}

// MarkChannelSeen records that a channel's history has been bootstrapped.
func (c *Context) MarkChannelSeen(channelID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seenChannels[channelID] = true
}

// ResetSeenChannels clears channel tracking (called after compaction).
func (c *Context) ResetSeenChannels() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seenChannels = make(map[string]bool)
}

// RemoveChannelHistory removes existing "[Recent history for channel=X]"
// system messages for the given channel so they can be replaced with fresh ones.
func (c *Context) RemoveChannelHistory(channelID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := "[Recent history for channel=" + channelID + "]"
	filtered := c.messages[:0]
	for _, m := range c.messages {
		if m.Role == "system" && len(m.Content) >= len(prefix) && m.Content[:len(prefix)] == prefix {
			continue
		}
		filtered = append(filtered, m)
	}
	c.messages = filtered
}

// HasMessagesForChannel はこのチャンネルの user/assistant メッセージが context にあるか返す。
func (c *Context) HasMessagesForChannel(channelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.messages {
		if m.Channel == channelID && (m.Role == "user" || m.Role == "assistant") {
			return true
		}
	}
	return false
}

// RemoveByChannel removes all messages belonging to the given channel ID.
// Returns the number of messages removed.
func (c *Context) RemoveByChannel(channelID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	before := len(c.messages)
	filtered := make([]llm.Message, 0, len(c.messages))
	for _, m := range c.messages {
		if m.Channel == channelID {
			continue
		}
		filtered = append(filtered, m)
	}
	c.messages = filtered
	return before - len(c.messages)
}

// Channels returns a list of unique channel IDs present in the context.
func (c *Context) Channels() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]bool)
	var channels []string
	for _, m := range c.messages {
		if m.Channel != "" && !seen[m.Channel] {
			seen[m.Channel] = true
			channels = append(channels, m.Channel)
		}
	}
	return channels
}

// MessagesForChannel returns messages filtered by channel.
// If channelID is empty, returns all messages (Device/Web compatibility).
// Otherwise returns messages where Channel matches channelID or Channel is empty.
// Tool call/result chains are kept intact: if an assistant message with ToolCalls
// is included, all corresponding tool results are also included, and vice versa.
func (c *Context) MessagesForChannel(channelID string) []llm.Message {
	if channelID == "" {
		return c.Messages()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	// First pass: collect messages matching the channel filter.
	matched := make(map[int]bool, len(c.messages))
	for i, m := range c.messages {
		if m.Channel == channelID || m.Channel == "" {
			matched[i] = true
		}
	}

	// Second pass: ensure tool call/result chain integrity.
	// Build maps: toolCallID -> assistant message index, toolCallID -> tool result index.
	callIDToAssistant := make(map[string]int)
	callIDToToolResult := make(map[string]int)
	for i, m := range c.messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				callIDToAssistant[tc.ID] = i
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			callIDToToolResult[m.ToolCallID] = i
		}
	}
	// If an assistant with tool_calls is matched, include all its tool results.
	for i, m := range c.messages {
		if !matched[i] || m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if idx, ok := callIDToToolResult[tc.ID]; ok {
				matched[idx] = true
			}
		}
	}
	// If a tool result is matched, include its triggering assistant message.
	for i, m := range c.messages {
		if !matched[i] || m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		if idx, ok := callIDToAssistant[m.ToolCallID]; ok {
			matched[idx] = true
		}
	}

	out := make([]llm.Message, 0, len(matched))
	for i, m := range c.messages {
		if matched[i] {
			out = append(out, m)
		}
	}
	return out
}

// MessagesWithSystemForChannel returns the system prompt followed by
// channel-filtered messages. Used for LLM calls after the first iteration.
func (c *Context) MessagesWithSystemForChannel(channelID string) []llm.Message {
	filtered := c.MessagesForChannel(channelID)
	sp := c.SystemPrompt()
	out := make([]llm.Message, 0, 1+len(filtered))
	if sp != "" {
		out = append(out, llm.Message{
			Role:    "system",
			Content: sp,
		})
	}
	out = append(out, filtered...)
	return out
}

// MaxTokens returns the configured max token limit.
func (c *Context) MaxTokens() int {
	return c.maxTokens
}

// SetMaxTokens updates the max token limit at runtime.
func (c *Context) SetMaxTokens(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxTokens = n
}

// ReplaceAll replaces all messages with the given slice.
func (c *Context) ReplaceAll(msgs []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = msgs
}

// CompactReplace atomically replaces the first snapshotLen messages
// with kept, preserving any messages appended after the snapshot.
func (c *Context) CompactReplace(snapshotLen int, kept []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []llm.Message
	result = append(result, kept...)
	if len(c.messages) > snapshotLen {
		result = append(result, c.messages[snapshotLen:]...)
	}
	c.messages = result
}

// TruncateOldest removes the oldest n messages (simple fallback compaction).
func (c *Context) TruncateOldest(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n >= len(c.messages) {
		c.messages = nil
		return
	}
	c.messages = c.messages[n:]
}
