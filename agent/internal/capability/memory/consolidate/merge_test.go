package consolidate

import (
	"testing"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
)

func TestBuildMergedMemory_MetadataUnion(t *testing.T) {
	d := decision{
		mergedContent: "統合された内容",
		groupType:     memory.MemoryTypeEpisode,
		sourceEntries: []memEntry{
			{
				metadata: map[string]any{
					"participants":   []string{"a", "b"},
					"emotional_tone": "楽しい",
				},
				persons:  []string{"a", "b"},
				keywords: []string{"Go"},
				topic:    "技術",
			},
			{
				metadata: map[string]any{
					"participants":   []string{"b", "c"},
					"emotional_tone": "嬉しい",
				},
				persons:  []string{"b", "c"},
				keywords: []string{"Python", "Go"},
				topic:    "技術/Python",
			},
		},
	}

	mem := buildMergedMemory(d)

	if mem.Content != "統合された内容" {
		t.Errorf("content = %q", mem.Content)
	}
	if mem.Type != memory.MemoryTypeEpisode {
		t.Errorf("type = %s", mem.Type)
	}

	// Persons は union: a, b, c
	if len(mem.Persons) != 3 {
		t.Errorf("Persons union = %v, want 3 elements", mem.Persons)
	}

	// Keywords は union: Go, Python
	if len(mem.Keywords) != 2 {
		t.Errorf("Keywords union = %v, want 2 elements", mem.Keywords)
	}

	// Topic は最新（2番目）のエントリから
	if mem.Topic != "技術/Python" {
		t.Errorf("Topic = %q, want 技術/Python", mem.Topic)
	}
}

func TestBuildMergedMemory_SourceTag(t *testing.T) {
	d := decision{
		mergedContent: "test",
		groupType:     memory.MemoryTypeWorld,
		sourceEntries: []memEntry{{metadata: nil}},
	}
	mem := buildMergedMemory(d)
	if mem.Metadata["source"] != MergeSourceTag {
		t.Errorf("source tag = %v, want %q", mem.Metadata["source"], MergeSourceTag)
	}
}

func TestMergeMemoryFields_EmptySources(t *testing.T) {
	meta, persons, keywords, topic := mergeMemoryFields(nil)
	if meta == nil {
		t.Error("meta should not be nil")
	}
	if len(persons) != 0 || len(keywords) != 0 || topic != "" {
		t.Errorf("空ソースでは全て空であるべき: persons=%v keywords=%v topic=%q", persons, keywords, topic)
	}
}
