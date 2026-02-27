package agent

import (
	"sync"

	"github.com/haryoiro/suzuha/internal/llm"
)

// Context manages the short-term message history (in-memory).
// The system prompt (IDENTITY.md) is stored separately from messages
// so it is never affected by compaction or truncation.
type Context struct {
	mu              sync.RWMutex
	messages        []llm.Message
	systemPrompt    string          // pinned system prompt, immune to compaction
	maxTokens       int
	injectedUsers   map[string]bool // tracks which user IDs have had profiles injected
	seenChannels    map[string]bool // tracks channels with bootstrapped history
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

// EstimatedTokens returns a rough token count (4 chars ≈ 1 token).
// Includes the pinned system prompt.
func (c *Context) EstimatedTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := 0
	if c.systemPrompt != "" {
		total += len(c.systemPrompt)/4 + 4
	}
	for _, m := range c.messages {
		total += len(m.Content)/4 + 4 // +4 for role overhead
	}
	return total
}

// UsageRatio returns estimated token usage as a fraction of max.
func (c *Context) UsageRatio() float64 {
	if c.maxTokens <= 0 {
		return 0
	}
	return float64(c.EstimatedTokens()) / float64(c.maxTokens)
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

// MaxTokens returns the configured max token limit.
func (c *Context) MaxTokens() int {
	return c.maxTokens
}

// ReplaceAll replaces all messages with the given slice.
func (c *Context) ReplaceAll(msgs []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = msgs
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
