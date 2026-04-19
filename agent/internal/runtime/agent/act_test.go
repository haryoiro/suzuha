package agent

import (
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/message"
)

func TestGroupByChannel(t *testing.T) {
	t.Run("active channel comes last", func(t *testing.T) {
		now := time.Now()
		msgs := []message.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "A1", Channel: "ch-a", Timestamp: now},
			{Role: "user", Content: "B1", Channel: "ch-b", Timestamp: now.Add(1 * time.Second)},
			{Role: "user", Content: "A2", Channel: "ch-a", Timestamp: now.Add(2 * time.Second)},
			{Role: "user", Content: "B2", Channel: "ch-b", Timestamp: now.Add(3 * time.Second)},
			{Role: "user", Content: "A3", Channel: "ch-a", Timestamp: now.Add(4 * time.Second)},
		}

		result := groupByChannel(msgs, "ch-a")

		if result[0].Role != "system" {
			t.Error("先頭は system であるべき")
		}

		bStart := -1
		aStart := -1
		for i, m := range result {
			if m.Channel == "ch-b" && bStart < 0 {
				bStart = i
			}
			if m.Channel == "ch-a" && aStart < 0 {
				aStart = i
			}
		}
		if bStart >= aStart {
			t.Errorf("ch-b が ch-a より前に来るべき: bStart=%d, aStart=%d", bStart, aStart)
		}

		var aContents []string
		for _, m := range result {
			if m.Channel == "ch-a" {
				aContents = append(aContents, m.Content)
			}
		}
		if len(aContents) != 3 || aContents[0] != "A1" || aContents[1] != "A2" || aContents[2] != "A3" {
			t.Errorf("ch-a 内の順序が維持されるべき: %v", aContents)
		}
	})

	t.Run("others sorted by recency", func(t *testing.T) {
		now := time.Now()
		msgs := []message.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "C1", Channel: "ch-c", Timestamp: now},
			{Role: "user", Content: "B1", Channel: "ch-b", Timestamp: now.Add(2 * time.Second)},
			{Role: "user", Content: "A1", Channel: "ch-a", Timestamp: now.Add(1 * time.Second)},
			{Role: "user", Content: "active", Channel: "ch-active", Timestamp: now.Add(3 * time.Second)},
		}

		result := groupByChannel(msgs, "ch-active")

		// system を除いた順序: ch-c (古) → ch-a (中) → ch-b (新) → ch-active (末尾)
		var channels []string
		for _, m := range result {
			if m.Role == "system" {
				continue
			}
			channels = append(channels, m.Channel)
		}
		expected := []string{"ch-c", "ch-a", "ch-b", "ch-active"}
		for i, ch := range expected {
			if i >= len(channels) || channels[i] != ch {
				t.Errorf("index %d: got %v, want %v (full: %v)", i, channels, expected, channels)
				break
			}
		}
	})

	t.Run("assistant message inherits channel", func(t *testing.T) {
		now := time.Now()
		msgs := []message.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello", Channel: "ch-a", Timestamp: now},
			{Role: "assistant", Content: "hi", Channel: "", Timestamp: now.Add(1 * time.Second)},
			{Role: "user", Content: "other", Channel: "ch-b", Timestamp: now.Add(2 * time.Second)},
		}

		result := groupByChannel(msgs, "ch-a")

		var aContents []string
		for _, m := range result {
			if m.Content == "hello" || m.Content == "hi" {
				aContents = append(aContents, m.Content)
			}
		}
		if len(aContents) != 2 {
			t.Errorf("assistant は ch-a に帰属すべき: %v", aContents)
		}

		for i := 0; i < len(result)-1; i++ {
			if result[i].Content == "hello" && result[i+1].Content != "hi" {
				t.Errorf("hello の次に hi が来るべき、got %q", result[i+1].Content)
			}
		}
	})

	t.Run("empty channel", func(t *testing.T) {
		msgs := []message.Message{
			{Role: "user", Content: "msg1"},
			{Role: "user", Content: "msg2"},
		}
		result := groupByChannel(msgs, "")
		if len(result) != len(msgs) {
			t.Error("activeChannel 空なら何もしない")
		}
	})

	t.Run("single channel", func(t *testing.T) {
		now := time.Now()
		msgs := []message.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "a", Channel: "ch-a", Timestamp: now},
			{Role: "user", Content: "b", Channel: "ch-a", Timestamp: now.Add(time.Second)},
		}
		result := groupByChannel(msgs, "ch-a")

		if result[0].Content != "sys" || result[1].Content != "a" || result[2].Content != "b" {
			t.Errorf("単一チャンネルで順序が変わるべきではない: %v", result)
		}
	})

	t.Run("directive system at end", func(t *testing.T) {
		now := time.Now()
		msgs := []message.Message{
			{Role: "system", Content: "prompt"},
			{Role: "user", Content: "B", Channel: "ch-b", Timestamp: now},
			{Role: "user", Content: "A", Channel: "ch-a", Timestamp: now.Add(time.Second)},
			{Role: "system", Content: "[LISTEN] directive", Timestamp: now.Add(2 * time.Second)},
		}

		result := groupByChannel(msgs, "ch-a")

		if result[0].Content != "prompt" {
			t.Errorf("[0] system prompt, got %q", result[0].Content)
		}
		if result[1].Content != "B" {
			t.Errorf("[1] ch-b, got %q", result[1].Content)
		}
		if result[2].Content != "A" {
			t.Errorf("[2] ch-a, got %q", result[2].Content)
		}
		if result[3].Content != "[LISTEN] directive" {
			t.Errorf("[3] directive, got %q", result[3].Content)
		}
	})
}
