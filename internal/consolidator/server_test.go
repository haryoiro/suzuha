package consolidator

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/memory"
)

func TestParseCompactResponse_KeepAndMemories(t *testing.T) {
	input := `KEEP: 0, 2, 5, 7

MEMORIES:
- [user] Likes Go programming
- [world] Tokyo is the capital of Japan
- [tool] fetch tool was used to get weather data`

	result := parseCompactResponse(input, 10)

	if len(result.KeepIndices) != 4 {
		t.Fatalf("expected 4 keep indices, got %d", len(result.KeepIndices))
	}
	expected := []int{0, 2, 5, 7}
	for i, idx := range result.KeepIndices {
		if idx != expected[i] {
			t.Errorf("keep index %d: expected %d, got %d", i, expected[i], idx)
		}
	}

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

func TestParseMemoryLine_WithUserID(t *testing.T) {
	mem, ok := parseMemoryLine("[user user_id=abc123] Likes Python programming")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeUser {
		t.Errorf("expected user type, got %s", mem.Type)
	}
	if mem.Content != "Likes Python programming" {
		t.Errorf("content: got %q", mem.Content)
	}
	if mem.Metadata == nil {
		t.Fatal("expected metadata to be non-nil")
	}
	if mem.Metadata["user_id"] != "abc123" {
		t.Errorf("user_id: got %v", mem.Metadata["user_id"])
	}
}

func TestParseMemoryLine_WithoutUserID(t *testing.T) {
	// Backward compatibility: [user] without user_id still works.
	mem, ok := parseMemoryLine("[user] Likes Go programming")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeUser {
		t.Errorf("expected user type, got %s", mem.Type)
	}
	if mem.Content != "Likes Go programming" {
		t.Errorf("content: got %q", mem.Content)
	}
	if mem.Metadata != nil {
		t.Errorf("expected nil metadata for [user] without user_id, got %v", mem.Metadata)
	}
}

func TestParseMemoryLine_WorldAndTool(t *testing.T) {
	world, ok := parseMemoryLine("[world] Tokyo is the capital")
	if !ok || world.Type != memory.MemoryTypeWorld {
		t.Errorf("world: ok=%v type=%s", ok, world.Type)
	}

	tool, ok := parseMemoryLine("[tool] fetch was used")
	if !ok || tool.Type != memory.MemoryTypeTool {
		t.Errorf("tool: ok=%v type=%s", ok, tool.Type)
	}
}

func TestParseMemoryLine_Episode(t *testing.T) {
	mem, ok := parseMemoryLine("[episode participants=123,456 tone=楽しい] アニメの話で盛り上がった")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeEpisode {
		t.Errorf("expected episode type, got %s", mem.Type)
	}
	if mem.Content != "アニメの話で盛り上がった" {
		t.Errorf("content: got %q", mem.Content)
	}
	participants, ok := mem.Metadata["participants"].([]string)
	if !ok || len(participants) != 2 {
		t.Errorf("participants: got %v", mem.Metadata["participants"])
	}
	if mem.Metadata["emotional_tone"] != "楽しい" {
		t.Errorf("tone: got %v", mem.Metadata["emotional_tone"])
	}
}

func TestParseMemoryLine_Self(t *testing.T) {
	mem, ok := parseMemoryLine("[self] プログラミングの話になると饒舌になる")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if mem.Type != memory.MemoryTypeSelf {
		t.Errorf("expected self type, got %s", mem.Type)
	}
	if mem.Content != "プログラミングの話になると饒舌になる" {
		t.Errorf("content: got %q", mem.Content)
	}
}

func TestParseCompactResponse_UserMemoriesWithUserID(t *testing.T) {
	input := `KEEP: 0, 1

MEMORIES:
- [user user_id=12345] Likes Python
- [user] General user fact without ID
- [world] Some world fact`

	result := parseCompactResponse(input, 5)

	if len(result.Memories) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(result.Memories))
	}

	// First memory has user_id in metadata.
	if result.Memories[0].Metadata == nil || result.Memories[0].Metadata["user_id"] != "12345" {
		t.Errorf("expected user_id=12345, got %v", result.Memories[0].Metadata)
	}

	// Second memory has no user_id.
	if result.Memories[1].Metadata != nil {
		t.Errorf("expected nil metadata, got %v", result.Memories[1].Metadata)
	}
}
