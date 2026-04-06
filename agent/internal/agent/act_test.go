package agent

import (
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
)

func TestGroupByChannel_ActiveChannelLast(t *testing.T) {
	now := time.Now()
	msgs := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "A1", Channel: "ch-a", Timestamp: now},
		{Role: "user", Content: "B1", Channel: "ch-b", Timestamp: now.Add(1 * time.Second)},
		{Role: "user", Content: "A2", Channel: "ch-a", Timestamp: now.Add(2 * time.Second)},
		{Role: "user", Content: "B2", Channel: "ch-b", Timestamp: now.Add(3 * time.Second)},
		{Role: "user", Content: "A3", Channel: "ch-a", Timestamp: now.Add(4 * time.Second)},
	}

	result := groupByChannel(msgs, "ch-a")

	// system は先頭
	if result[0].Role != "system" {
		t.Error("先頭は system であるべき")
	}

	// ch-b が先、ch-a が後
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

	// ch-a 内の順序は維持
	var aContents []string
	for _, m := range result {
		if m.Channel == "ch-a" {
			aContents = append(aContents, m.Content)
		}
	}
	if len(aContents) != 3 || aContents[0] != "A1" || aContents[1] != "A2" || aContents[2] != "A3" {
		t.Errorf("ch-a 内の順序が維持されるべき: %v", aContents)
	}
}

func TestGroupByChannel_OthersSortedByRecency(t *testing.T) {
	now := time.Now()
	msgs := []llm.Message{
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
}

func TestGroupByChannel_AssistantMsgInheritsChannel(t *testing.T) {
	now := time.Now()
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello", Channel: "ch-a", Timestamp: now},
		{Role: "assistant", Content: "hi", Channel: "", Timestamp: now.Add(1 * time.Second)},
		{Role: "user", Content: "other", Channel: "ch-b", Timestamp: now.Add(2 * time.Second)},
	}

	result := groupByChannel(msgs, "ch-a")

	// assistant (Channel空) は ch-a に帰属 → ch-a グループに含まれる
	var aContents []string
	// ch-a は末尾にまとまる
	for _, m := range result {
		if m.Content == "hello" || m.Content == "hi" {
			aContents = append(aContents, m.Content)
		}
	}
	if len(aContents) != 2 {
		t.Errorf("assistant は ch-a に帰属すべき: %v", aContents)
	}

	// hello と hi は連続しているべき
	for i := 0; i < len(result)-1; i++ {
		if result[i].Content == "hello" && result[i+1].Content != "hi" {
			t.Errorf("hello の次に hi が来るべき、got %q", result[i+1].Content)
		}
	}
}

func TestGroupByChannel_EmptyChannel(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "msg1"},
		{Role: "user", Content: "msg2"},
	}
	result := groupByChannel(msgs, "")
	if len(result) != len(msgs) {
		t.Error("activeChannel 空なら何もしない")
	}
}

func TestGroupByChannel_SingleChannel(t *testing.T) {
	now := time.Now()
	msgs := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "a", Channel: "ch-a", Timestamp: now},
		{Role: "user", Content: "b", Channel: "ch-a", Timestamp: now.Add(time.Second)},
	}
	result := groupByChannel(msgs, "ch-a")

	// 単一チャンネルなら順序変わらない
	if result[0].Content != "sys" || result[1].Content != "a" || result[2].Content != "b" {
		t.Errorf("単一チャンネルで順序が変わるべきではない: %v", result)
	}
}

func TestGroupByChannel_DirectiveSystemAtEnd(t *testing.T) {
	// 実際の構造: system prompt → 会話 → directive (末尾)
	// directive の直前は activeChannel のメッセージ。
	now := time.Now()
	msgs := []llm.Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "B", Channel: "ch-b", Timestamp: now},
		{Role: "user", Content: "A", Channel: "ch-a", Timestamp: now.Add(time.Second)},
		{Role: "system", Content: "[LISTEN] directive", Timestamp: now.Add(2 * time.Second)},
	}

	result := groupByChannel(msgs, "ch-a")

	// system prompt → ch-b → ch-a + directive
	if result[0].Content != "prompt" {
		t.Errorf("[0] system prompt, got %q", result[0].Content)
	}
	if result[1].Content != "B" {
		t.Errorf("[1] ch-b, got %q", result[1].Content)
	}
	if result[2].Content != "A" {
		t.Errorf("[2] ch-a, got %q", result[2].Content)
	}
	// directive は直前の ch-a に帰属し、ch-a グループの末尾に来る
	if result[3].Content != "[LISTEN] directive" {
		t.Errorf("[3] directive, got %q", result[3].Content)
	}
}
