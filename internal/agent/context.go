package agent

import (
	"sync"

	"github.com/haryoiro/suzuha/internal/llm"
)

// Context manages the short-term message history (in-memory).
type Context struct {
	mu       sync.RWMutex
	messages []llm.Message
	maxTokens int
}

// NewContext creates a context manager.
func NewContext(maxTokens int) *Context {
	return &Context{maxTokens: maxTokens}
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
func (c *Context) EstimatedTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	total := 0
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
