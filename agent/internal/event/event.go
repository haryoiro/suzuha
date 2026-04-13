package event

import (
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
)

// EventFactory はタイムゾーンを保持するイベント生成ユーティリティ。
type EventFactory struct {
	Clock *jtime.Clock
}

// NewEventFactory は指定された Clock でイベントファクトリを生成する。
func NewEventFactory(clock *jtime.Clock) *EventFactory {
	return &EventFactory{Clock: clock}
}

// MessagePayload carries typed fields for a chat message event.
type MessagePayload struct {
	Content     string   `json:"content"`
	Channel     string   `json:"channel"`
	ChannelName string   `json:"channel_name,omitempty"`
	UserID      string   `json:"user_id"`
	UserName    string   `json:"user_name"`
	MessageID   string   `json:"message_id,omitempty"`
	GuildID     string   `json:"guild_id,omitempty"`
	GuildName   string   `json:"guild_name,omitempty"`
	ImageURLs   []string `json:"image_urls,omitempty"`
	IsDM        bool     `json:"is_dm"`
	IsMention   bool     `json:"is_mention"`
	IsBot       bool     `json:"is_bot,omitempty"`
	IsVoice     bool     `json:"is_voice,omitempty"`
}

// Well-known event sources and types.
const (
	SourceInternal = "internal"
	TypeSelfPrompt = "self_prompt"
)

// Event is the common schema for all events from all sources.
type Event struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"`    // "discord" | "cli" | "internal"
	Type      string         `json:"type"`      // "message" | "self_prompt"
	Message   MessagePayload `json:"message"`
	Timestamp time.Time      `json:"timestamp"`
}

// NewMessageEvent creates a message event with a generated ID and current timestamp.
func NewMessageEvent(clock *jtime.Clock, source string, msg MessagePayload) Event {
	return Event{
		ID:        uuid.NewString(),
		Source:    source,
		Type:      "message",
		Message:   msg,
		Timestamp: clock.Now(),
	}
}

// NewSelfPromptEvent creates an internal self-prompt event (e.g. boredom trigger).
// These events are processed by the agent pipeline but do not count as user interaction.
func NewSelfPromptEvent(clock *jtime.Clock, channel, content string) Event {
	return Event{
		ID:        uuid.NewString(),
		Source:    SourceInternal,
		Type:      TypeSelfPrompt,
		Message:   MessagePayload{Content: content, Channel: channel},
		Timestamp: clock.Now(),
	}
}

// Bus is a simple fan-in event bus. All sources publish events,
// the agent consumes them from a single channel.
type Bus struct {
	ch chan Event
}

func NewBus(bufferSize int) *Bus {
	return &Bus{ch: make(chan Event, bufferSize)}
}

// Publish sends an event to the bus. Non-blocking if buffer has space.
func (b *Bus) Publish(e Event) {
	b.ch <- e
}

// Subscribe returns the read-only channel for consuming events.
func (b *Bus) Subscribe() <-chan Event {
	return b.ch
}
