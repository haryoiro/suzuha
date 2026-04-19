package consolidate

import (
	"strings"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
)

func TestBuildJudgePrompt_SingleGroup(t *testing.T) {
	groups := []memoryGroup{{
		memType: memory.MemoryTypeUser,
		members: []memEntry{
			{id: "abc", content: "Goが好き", createdAt: time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC), metadata: map[string]any{"user_id": "123"}},
			{id: "def", content: "Go言語が好き", createdAt: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), metadata: map[string]any{"user_id": "123"}},
		},
	}}
	prompt := buildJudgePrompt(groups)
	if !strings.Contains(prompt, "グループ 1") {
		t.Error("グループ番号が含まれるべき")
	}
	if !strings.Contains(prompt, "id=abc") || !strings.Contains(prompt, "id=def") {
		t.Error("メンバーIDが含まれるべき")
	}
	if !strings.Contains(prompt, "user_id=123") {
		t.Error("メタデータが含まれるべき")
	}
}

func TestBuildJudgePrompt_MultipleGroups(t *testing.T) {
	groups := []memoryGroup{
		{memType: memory.MemoryTypeUser, members: []memEntry{{id: "a", content: "test", createdAt: time.Now()}}},
		{memType: memory.MemoryTypeWorld, members: []memEntry{{id: "b", content: "test2", createdAt: time.Now()}}},
	}
	prompt := buildJudgePrompt(groups)
	if !strings.Contains(prompt, "グループ 1") || !strings.Contains(prompt, "グループ 2") {
		t.Error("全グループの番号が含まれるべき")
	}
}

func TestParseDecisions_Keep(t *testing.T) {
	raw := `[{"group":1,"action":"keep","keep_id":"b","reason":"bの方が詳しい"}]`
	groups := []memoryGroup{{
		memType: memory.MemoryTypeUser,
		members: []memEntry{{id: "a"}, {id: "b"}},
	}}
	decs, err := parseDecisions(raw, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) != 1 {
		t.Fatalf("1件の判定期待, got %d", len(decs))
	}
	if decs[0].action != "keep" || decs[0].keepID != "b" {
		t.Errorf("keep action with keepID=b 期待, got %+v", decs[0])
	}
	if len(decs[0].deleteIDs) != 1 || decs[0].deleteIDs[0] != "a" {
		t.Errorf("deleteIDs=[a] 期待, got %v", decs[0].deleteIDs)
	}
}

func TestParseDecisions_Merge(t *testing.T) {
	raw := `[{"group":1,"action":"merge","merged_content":"統合テスト","reason":"類似"}]`
	groups := []memoryGroup{{
		memType: memory.MemoryTypeUser,
		members: []memEntry{{id: "a"}, {id: "b"}},
	}}
	decs, err := parseDecisions(raw, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) != 1 || decs[0].action != "merge" {
		t.Fatalf("merge 判定期待, got %+v", decs)
	}
	if decs[0].mergedContent != "統合テスト" {
		t.Errorf("mergedContent 期待, got %q", decs[0].mergedContent)
	}
	if len(decs[0].deleteIDs) != 2 {
		t.Errorf("全メンバーIDが deleteIDs に含まれるべき, got %v", decs[0].deleteIDs)
	}
}

func TestParseDecisions_Skip(t *testing.T) {
	raw := `[{"group":1,"action":"skip","reason":"異なる内容"}]`
	groups := []memoryGroup{{
		memType: memory.MemoryTypeUser,
		members: []memEntry{{id: "a"}, {id: "b"}},
	}}
	decs, err := parseDecisions(raw, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) != 0 {
		t.Errorf("skip は判定を生成しない, got %d", len(decs))
	}
}

func TestParseDecisions_InvalidJSON(t *testing.T) {
	_, err := parseDecisions("not json", nil)
	if err == nil {
		t.Error("不正な JSON でエラーを期待")
	}
}

func TestParseDecisions_OutOfRangeGroup(t *testing.T) {
	raw := `[{"group":99,"action":"keep","keep_id":"a"}]`
	groups := []memoryGroup{{
		memType: memory.MemoryTypeUser,
		members: []memEntry{{id: "a"}},
	}}
	decs, err := parseDecisions(raw, groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) != 0 {
		t.Errorf("範囲外グループはスキップすべき, got %d", len(decs))
	}
}

func TestFormatMaintainMetadata_AllFields(t *testing.T) {
	meta := map[string]any{
		"user_id":        "123",
		"participants":   []string{"a", "b"},
		"emotional_tone": "楽しい",
	}
	result := formatMaintainMetadata(meta)
	if !strings.Contains(result, "user_id=123") {
		t.Error("user_id が含まれるべき")
	}
	if !strings.Contains(result, "participants=a,b") {
		t.Error("participants が含まれるべき")
	}
	if !strings.Contains(result, "tone=楽しい") {
		t.Error("tone が含まれるべき")
	}
}

func TestFormatMaintainMetadata_NilMeta(t *testing.T) {
	result := formatMaintainMetadata(nil)
	if result != "" {
		t.Errorf("nil meta では空文字列を期待, got %q", result)
	}
}
