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

func TestParseCompactResponse_WithAffinity(t *testing.T) {
	input := `KEEP: 0, 1

MEMORIES:
- [user] User enjoys talking about cats

AFFINITY:
- [delta] user_id=12345 platform=discord delta=+0.5 messages=1,3,5 reason=楽しい会話をした
- [delta] user_id=67890 platform=discord delta=-0.2 messages=4 reason=失礼な発言`

	result := parseCompactResponse(input, 10)

	if len(result.AffinityDeltas) != 2 {
		t.Fatalf("expected 2 affinity deltas, got %d", len(result.AffinityDeltas))
	}

	d1 := result.AffinityDeltas[0]
	if d1.PlatformUserID != "12345" {
		t.Errorf("expected user_id=12345, got %s", d1.PlatformUserID)
	}
	if d1.Platform != "discord" {
		t.Errorf("expected platform=discord, got %s", d1.Platform)
	}
	if d1.Delta != 0.5 {
		t.Errorf("expected delta=0.5, got %f", d1.Delta)
	}
	if len(d1.MessageIndices) != 3 {
		t.Errorf("expected 3 message indices, got %d", len(d1.MessageIndices))
	}
	if d1.Reason != "楽しい会話をした" {
		t.Errorf("expected reason=楽しい会話をした, got %s", d1.Reason)
	}

	d2 := result.AffinityDeltas[1]
	if d2.Delta != -0.2 {
		t.Errorf("expected delta=-0.2, got %f", d2.Delta)
	}
	if d2.Reason != "失礼な発言" {
		t.Errorf("expected reason=失礼な発言, got %s", d2.Reason)
	}
}

func TestParseCompactResponse_NoAffinity(t *testing.T) {
	input := `KEEP: 0, 1

MEMORIES:
- [user] Some user fact`

	result := parseCompactResponse(input, 5)

	if len(result.AffinityDeltas) != 0 {
		t.Errorf("expected 0 affinity deltas, got %d", len(result.AffinityDeltas))
	}
}

func TestParseAffinityDelta_Valid(t *testing.T) {
	d, ok := parseAffinityDelta("[delta] user_id=abc123 platform=discord delta=+0.3 messages=1,2 reason=nice chat")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d.PlatformUserID != "abc123" {
		t.Errorf("user_id: got %s", d.PlatformUserID)
	}
	if d.Platform != "discord" {
		t.Errorf("platform: got %s", d.Platform)
	}
	if d.Delta != 0.3 {
		t.Errorf("delta: got %f", d.Delta)
	}
	if len(d.MessageIndices) != 2 {
		t.Errorf("message indices: got %d", len(d.MessageIndices))
	}
	if d.Reason != "nice chat" {
		t.Errorf("reason: got %q", d.Reason)
	}
}

func TestParseAffinityDelta_InvalidPrefix(t *testing.T) {
	_, ok := parseAffinityDelta("[user] some text")
	if ok {
		t.Fatal("expected ok=false for non-delta prefix")
	}
}

func TestParseAffinityDelta_MissingUserID(t *testing.T) {
	_, ok := parseAffinityDelta("[delta] platform=discord delta=+0.1 reason=test")
	if ok {
		t.Fatal("expected ok=false for missing user_id")
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

func TestParseAffinityDelta_WithAxis(t *testing.T) {
	d, ok := parseAffinityDelta("[delta] user_id=abc123 platform=discord axis=trust delta=+0.5 messages=1 reason=(感) 秘密を打ち明けた")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d.Axis != "trust" {
		t.Errorf("axis: expected trust, got %s", d.Axis)
	}
	if d.Delta != 0.5 {
		t.Errorf("delta: got %f", d.Delta)
	}
	if d.Reason != "(感) 秘密を打ち明けた" {
		t.Errorf("reason: got %q", d.Reason)
	}
}

func TestParseAffinityDelta_DefaultAxis(t *testing.T) {
	d, ok := parseAffinityDelta("[delta] user_id=abc platform=discord delta=+0.1 reason=test")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if d.Axis != "closeness" {
		t.Errorf("axis: expected closeness (default), got %s", d.Axis)
	}
}

func TestParseAffinityDelta_InvalidAxis(t *testing.T) {
	d, ok := parseAffinityDelta("[delta] user_id=abc platform=discord axis=bogus delta=+0.1 reason=test")
	if !ok {
		t.Fatal("expected ok=true")
	}
	// Invalid axis should keep default
	if d.Axis != "closeness" {
		t.Errorf("axis: expected closeness (default for invalid), got %s", d.Axis)
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

func TestParseCompactResponse_MultiAxisAffinity(t *testing.T) {
	input := `KEEP: 0, 1

MEMORIES:
- [user user_id=123] Likes anime

AFFINITY:
- [delta] user_id=123 platform=discord axis=closeness delta=+0.3 messages=1 reason=(楽) 楽しく会話した
- [delta] user_id=123 platform=discord axis=interest delta=+0.5 messages=1 reason=(興) 面白い話題を提供`

	result := parseCompactResponse(input, 5)

	if len(result.AffinityDeltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(result.AffinityDeltas))
	}
	if result.AffinityDeltas[0].Axis != "closeness" {
		t.Errorf("delta[0] axis: got %s", result.AffinityDeltas[0].Axis)
	}
	if result.AffinityDeltas[1].Axis != "interest" {
		t.Errorf("delta[1] axis: got %s", result.AffinityDeltas[1].Axis)
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
