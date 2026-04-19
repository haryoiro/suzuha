package acquire

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
)

// --- JSON parser tests ---

func TestParseExtractedMemories_Basic(t *testing.T) {
	input := `[
		{"type":"user","content":"user_id=12345のたろうはPythonが好き","keywords":["Python"],"topic":"技術","persons":["12345"],"event_time":null},
		{"type":"world","content":"東京は日本の首都","keywords":["東京","日本"],"topic":"一般知識","persons":[]},
		{"type":"tool","content":"fetchツールで天気データを取得した","keywords":["fetch","天気"],"topic":"ツール","persons":[]}
	]`

	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mems) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(mems))
	}
	if mems[0].Type != memory.MemoryTypeUser {
		t.Errorf("expected user type, got %s", mems[0].Type)
	}
	if mems[1].Type != memory.MemoryTypeWorld {
		t.Errorf("expected world type, got %s", mems[1].Type)
	}
	if mems[2].Type != memory.MemoryTypeTool {
		t.Errorf("expected tool type, got %s", mems[2].Type)
	}
}

func TestParseExtractedMemories_WithCodeFence(t *testing.T) {
	input := "```json\n" + `[{"type":"world","content":"テスト","keywords":[],"topic":"","persons":[]}]` + "\n```"
	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(mems))
	}
}

func TestParseExtractedMemories_EmptyArray(t *testing.T) {
	mems, err := parseExtractedMemories("[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mems) != 0 {
		t.Fatalf("expected 0 memories, got %d", len(mems))
	}
}

func TestParseExtractedMemories_UserPersons(t *testing.T) {
	input := `[{"type":"user","content":"Likes Python","keywords":[],"topic":"","persons":["abc123"]}]`
	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatal(err)
	}
	// Persons カラムに格納される（metadata への併記は廃止済み）。
	if len(mems[0].Persons) != 1 || mems[0].Persons[0] != "abc123" {
		t.Errorf("expected Persons=[abc123], got %v", mems[0].Persons)
	}
}

func TestParseExtractedMemories_EpisodePersons(t *testing.T) {
	input := `[{"type":"episode","content":"アニメの話で盛り上がった","keywords":["アニメ"],"topic":"趣味","persons":["123","456"],"emotional_tone":"楽しい"}]`
	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatal(err)
	}
	if mems[0].Type != memory.MemoryTypeEpisode {
		t.Errorf("expected episode type, got %s", mems[0].Type)
	}
	// Persons カラムに格納される。
	if len(mems[0].Persons) != 2 {
		t.Errorf("expected Persons=[123,456], got %v", mems[0].Persons)
	}
	// emotional_tone は引き続き metadata に。
	if mems[0].Metadata["emotional_tone"] != "楽しい" {
		t.Errorf("expected emotional_tone, got %v", mems[0].Metadata["emotional_tone"])
	}
}

func TestParseExtractedMemories_WithEventTime(t *testing.T) {
	input := `[{"type":"episode","content":"テスト","keywords":[],"topic":"","persons":[],"event_time":"2026-03-30T21:00:00+09:00"}]`
	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatal(err)
	}
	if mems[0].EventTime == nil {
		t.Fatal("expected non-nil EventTime")
	}
	if mems[0].EventTime.Year() != 2026 || mems[0].EventTime.Month() != 3 || mems[0].EventTime.Day() != 30 {
		t.Errorf("unexpected EventTime: %v", mems[0].EventTime)
	}
}

func TestParseExtractedMemories_EmptyContent(t *testing.T) {
	input := `[{"type":"world","content":"","keywords":[],"topic":"","persons":[]}]`
	mems, err := parseExtractedMemories(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(mems) != 0 {
		t.Errorf("expected 0 memories (empty content filtered), got %d", len(mems))
	}
}

// --- Legacy parser tests ---

func TestParseLegacyCompactResponse_MemoriesOnly(t *testing.T) {
	input := `MEMORIES:
- [user] Likes Go programming
- [world] Tokyo is the capital of Japan
- [tool] fetch tool was used to get weather data`

	result := parseLegacyCompactResponse(input)

	if len(result.Memories) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(result.Memories))
	}
	if result.Memories[0].Type != memory.MemoryTypeUser {
		t.Errorf("expected user type, got %s", result.Memories[0].Type)
	}
	if result.Memories[1].Type != memory.MemoryTypeWorld {
		t.Errorf("expected world type, got %s", result.Memories[1].Type)
	}
	if result.Memories[2].Type != memory.MemoryTypeTool {
		t.Errorf("expected tool type, got %s", result.Memories[2].Type)
	}
}

func TestParseLegacyCompactResponse_EmptyMemories(t *testing.T) {
	result := parseLegacyCompactResponse(`MEMORIES:`)
	if len(result.Memories) != 0 {
		t.Fatalf("expected 0 memories, got %d", len(result.Memories))
	}
}

func TestParseLegacyMemoryLine_WithUserID(t *testing.T) {
	mem, ok := parseLegacyMemoryLine("[user user_id=abc123] Likes Python programming")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeUser {
		t.Errorf("expected user type, got %s", mem.Type)
	}
	if mem.Content != "Likes Python programming" {
		t.Errorf("content: got %q", mem.Content)
	}
	if mem.Metadata == nil || mem.Metadata["user_id"] != "abc123" {
		t.Errorf("user_id: got %v", mem.Metadata)
	}
}

func TestParseLegacyMemoryLine_Episode(t *testing.T) {
	mem, ok := parseLegacyMemoryLine("[episode participants=123,456 tone=楽しい] アニメの話で盛り上がった")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeEpisode {
		t.Errorf("expected episode type, got %s", mem.Type)
	}
	participants, ok := mem.Metadata["participants"].([]string)
	if !ok || len(participants) != 2 {
		t.Errorf("participants: got %v", mem.Metadata["participants"])
	}
}

// --- Prompt builder tests ---

func TestBuildSystemPrompt_WithRules(t *testing.T) {
	prompt := buildSystemPrompt([]ExtractionRule{Disambiguation})
	if !containsAll(prompt, "曖昧さ排除", "代名詞禁止", "相対時間禁止") {
		t.Error("expected disambiguation rule sections in system prompt")
	}
}

func TestBuildSystemPrompt_NoRules(t *testing.T) {
	prompt := buildSystemPrompt(nil)
	if len(prompt) == 0 {
		t.Error("expected non-empty base prompt")
	}
	if containsAll(prompt, "曖昧さ排除") {
		t.Error("expected no disambiguation rule when rules is nil")
	}
}

func TestBuildCompactPrompt_WithExistingMemories(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "hello", UserID: "123", Source: "discord", UserName: "test"},
	}
	existing := []memory.Memory{
		{Type: memory.MemoryTypeUser, Content: "テストメモリ"},
	}
	prompt := buildCompactPrompt(msgs, existing)
	if !containsAll(prompt, "既存メモリ", "テストメモリ", "重複を避ける") {
		t.Error("expected existing memory section in prompt")
	}
}

func TestBuildCompactPrompt_NoExistingMemories(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "hello"},
	}
	prompt := buildCompactPrompt(msgs, nil)
	if containsAll(prompt, "既存メモリ") {
		t.Error("expected no existing memory section when none provided")
	}
}

// --- helpers ---

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
