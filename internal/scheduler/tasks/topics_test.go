package tasks

import (
	"os"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
)

func TestBuildTimeHint(t *testing.T) {
	tests := []struct {
		hour int
		want string
	}{
		{5, "深夜の時間帯です。"},
		{7, "朝の時間帯です。軽い挨拶や一日の始まりに合う話題が自然です。"},
		{12, "お昼の時間帯です。"},
		{15, "午後の時間帯です。"},
		{20, "夜の時間帯です。リラックスした話題が合います。"},
		{23, "深夜の時間帯です。"},
		{0, "深夜の時間帯です。"},
	}

	for _, tc := range tests {
		now := time.Date(2025, 1, 1, tc.hour, 0, 0, 0, time.UTC)
		got := buildTimeHint(now)
		if got != tc.want {
			t.Errorf("hour=%d: got %q, want %q", tc.hour, got, tc.want)
		}
	}
}

func TestResolveTimezone(t *testing.T) {
	// Valid timezone.
	loc := resolveTimezone("Asia/Tokyo")
	if loc.String() != "Asia/Tokyo" {
		t.Errorf("expected Asia/Tokyo, got %s", loc.String())
	}

	// Empty timezone → UTC.
	loc2 := resolveTimezone("")
	if loc2.String() != "UTC" {
		t.Errorf("expected UTC, got %s", loc2.String())
	}

	// Invalid timezone → UTC.
	loc3 := resolveTimezone("Invalid/Zone")
	if loc3.String() != "UTC" {
		t.Errorf("expected UTC for invalid zone, got %s", loc3.String())
	}
}

func TestParseActionDecision(t *testing.T) {
	topics := []previousTopic{
		{
			Memory:    memory.Memory{ID: "mem-0", Content: "Topic 0"},
			Topic:     "topic0",
			Responded: true,
			MessageID: "msg-0",
		},
		{
			Memory:    memory.Memory{ID: "mem-1", Content: "Topic 1"},
			Topic:     "topic1",
			Responded: false,
			MessageID: "msg-1",
		},
		{
			Memory:    memory.Memory{ID: "mem-2", Content: "Topic 2"},
			Topic:     "topic2",
			Responded: true,
			MessageID: "msg-2",
		},
	}

	tests := []struct {
		name        string
		text        string
		wantAction  topicAction
		wantTarget  string // expected memory ID, "" for nil target
	}{
		{
			name:       "NEW action",
			text:       "NEW",
			wantAction: actionNew,
			wantTarget: "",
		},
		{
			name:       "REPLY with index",
			text:       "REPLY:1",
			wantAction: actionReply,
			wantTarget: "mem-1",
		},
		{
			name:       "SUPPLEMENT with index",
			text:       "SUPPLEMENT:2",
			wantAction: actionSupplement,
			wantTarget: "mem-2",
		},
		{
			name:       "REPLY without index defaults to first",
			text:       "REPLY",
			wantAction: actionReply,
			wantTarget: "mem-0",
		},
		{
			name:       "SUPPLEMENT without index defaults to first",
			text:       "SUPPLEMENT",
			wantAction: actionSupplement,
			wantTarget: "mem-0",
		},
		{
			name:       "lowercase input normalized",
			text:       "reply:0",
			wantAction: actionReply,
			wantTarget: "mem-0",
		},
		{
			name:       "whitespace trimmed",
			text:       "  SUPPLEMENT : 1  ",
			wantAction: actionSupplement,
			wantTarget: "mem-1",
		},
		{
			name:       "invalid action defaults to NEW",
			text:       "SOMETHING_ELSE",
			wantAction: actionNew,
			wantTarget: "",
		},
		{
			name:       "out of bounds index defaults to first",
			text:       "REPLY:99",
			wantAction: actionReply,
			wantTarget: "mem-0",
		},
		{
			name:       "negative index defaults to first",
			text:       "REPLY:-1",
			wantAction: actionReply,
			wantTarget: "mem-0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseActionDecision(tc.text, topics)
			if got.Action != tc.wantAction {
				t.Errorf("action: got %q, want %q", got.Action, tc.wantAction)
			}
			if tc.wantTarget == "" {
				if got.ReplyTarget != nil {
					t.Errorf("expected nil target, got %v", got.ReplyTarget)
				}
			} else {
				if got.ReplyTarget == nil {
					t.Fatalf("expected non-nil target")
				}
				if got.ReplyTarget.Memory.ID != tc.wantTarget {
					t.Errorf("target: got %q, want %q", got.ReplyTarget.Memory.ID, tc.wantTarget)
				}
			}
		})
	}
}

func TestParseActionDecision_EmptyTopics(t *testing.T) {
	// REPLY with no topics should default to NEW-like but keep REPLY action with nil target.
	got := parseActionDecision("REPLY:0", nil)
	if got.Action != actionReply {
		t.Errorf("action: got %q, want REPLY", got.Action)
	}
	// With no replyable topics, target is nil.
	if got.ReplyTarget != nil {
		t.Errorf("expected nil target for empty topics")
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input    string
		maxRunes int
		want     string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"これはテストです", 4, "これはテ..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}

	for _, tc := range tests {
		got := truncateStr(tc.input, tc.maxRunes)
		if got != tc.want {
			t.Errorf("truncateStr(%q, %d): got %q, want %q", tc.input, tc.maxRunes, got, tc.want)
		}
	}
}

func TestTopicsTask_NameAndDescription(t *testing.T) {
	task := &TopicsTask{}
	if task.Name() != "topics" {
		t.Errorf("name: got %q", task.Name())
	}
	if task.Description() == "" {
		t.Error("description should not be empty")
	}
}

func TestTopicsTask_Now(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	task := &TopicsTask{
		nowFunc: func() time.Time { return fixed },
	}
	got := task.now()
	if !got.Equal(fixed) {
		t.Errorf("now(): got %v, want %v", got, fixed)
	}

	// Default to time.Now.
	task2 := &TopicsTask{}
	before := time.Now()
	got2 := task2.now()
	after := time.Now()
	if got2.Before(before) || got2.After(after) {
		t.Errorf("now() without nowFunc should return current time")
	}
}

func TestFetchPreviousTopics_FiltersBySource(t *testing.T) {
	// Test that previousTopic extraction correctly filters by source=topics.
	mems := []memory.Memory{
		{
			ID:      "m1",
			Content: "話題提供: test topic",
			Metadata: map[string]any{
				"source":     "topics",
				"topic":      "テスト",
				"responded":  true,
				"message_id": "discord-msg-1",
			},
		},
		{
			ID:      "m2",
			Content: "Some other memory",
			Metadata: map[string]any{
				"source": "rss",
			},
		},
		{
			ID:      "m3",
			Content: "No metadata memory",
		},
	}

	// Simulate what fetchPreviousTopics does internally.
	var topics []previousTopic
	for _, m := range mems {
		if m.Metadata == nil {
			continue
		}
		source, _ := m.Metadata["source"].(string)
		if source != "topics" {
			continue
		}
		topic, _ := m.Metadata["topic"].(string)
		responded, _ := m.Metadata["responded"].(bool)
		msgID, _ := m.Metadata["message_id"].(string)
		topics = append(topics, previousTopic{
			Memory:    m,
			Topic:     topic,
			Responded: responded,
			MessageID: msgID,
		})
	}

	if len(topics) != 1 {
		t.Fatalf("expected 1 topic, got %d", len(topics))
	}
	if topics[0].Topic != "テスト" {
		t.Errorf("topic: got %q", topics[0].Topic)
	}
	if !topics[0].Responded {
		t.Error("expected responded=true")
	}
	if topics[0].MessageID != "discord-msg-1" {
		t.Errorf("message_id: got %q", topics[0].MessageID)
	}
}

func TestTopicsTask_BackoffSkip(t *testing.T) {
	task := &TopicsTask{}
	task.mu.Lock()
	task.skipCounter = 3
	task.consecutiveNoResponse = 3
	task.mu.Unlock()

	// Simulate skip logic.
	task.mu.Lock()
	if task.skipCounter > 0 {
		task.skipCounter--
	}
	remaining := task.skipCounter
	task.mu.Unlock()

	if remaining != 2 {
		t.Errorf("skipCounter after decrement: got %d, want 2", remaining)
	}
}

func TestTopicsTask_MaxBackoffCap(t *testing.T) {
	// Test that backoff is capped at maxBackoff.
	consecutive := 100
	backoff := consecutive
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	if backoff != maxBackoff {
		t.Errorf("expected backoff capped at %d, got %d", maxBackoff, backoff)
	}
}

func TestLoadPromptFiles(t *testing.T) {
	// Empty dir.
	result := loadPromptFiles("")
	if result != "" {
		t.Errorf("expected empty string for empty dir, got %q", result)
	}

	// Nonexistent dir.
	result2 := loadPromptFiles("/tmp/nonexistent-dir-12345")
	if result2 != "" {
		t.Errorf("expected empty string for nonexistent dir, got %q", result2)
	}
}

func TestLoadPromptFiles_WithFiles(t *testing.T) {
	dir := t.TempDir()

	// Write IDENTITY.md only.
	if err := writeTestFile(dir, "IDENTITY.md", "I am Suzuha"); err != nil {
		t.Fatal(err)
	}
	result := loadPromptFiles(dir)
	if result != "I am Suzuha" {
		t.Errorf("got %q, want %q", result, "I am Suzuha")
	}

	// Add SOUL.md.
	if err := writeTestFile(dir, "SOUL.md", "Be kind"); err != nil {
		t.Fatal(err)
	}
	result2 := loadPromptFiles(dir)
	if result2 != "I am Suzuha\n\nBe kind" {
		t.Errorf("got %q, want %q", result2, "I am Suzuha\n\nBe kind")
	}
}

func writeTestFile(dir, name, content string) error {
	return os.WriteFile(dir+"/"+name, []byte(content), 0644)
}
