package memento

import (
	"math"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
)

func TestCosineDistance_Identical(t *testing.T) {
	v := []float32{1, 2, 3}
	d := cosineDistance(v, v)
	if d > 1e-6 {
		t.Errorf("同一ベクトルの距離は0であるべき, got %f", d)
	}
}

func TestCosineDistance_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	d := cosineDistance(a, b)
	if math.Abs(d-1.0) > 1e-6 {
		t.Errorf("直交ベクトルの距離は1.0であるべき, got %f", d)
	}
}

func TestCosineDistance_DifferentLengths(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	d := cosineDistance(a, b)
	if d != 2.0 {
		t.Errorf("異なる長さでは2.0を返すべき, got %f", d)
	}
}

func TestCosineDistance_ZeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 2, 3}
	d := cosineDistance(a, b)
	if d != 2.0 {
		t.Errorf("ゼロベクトルでは2.0を返すべき, got %f", d)
	}
}

func TestBuildSimilarityGroups_NoGroups(t *testing.T) {
	entries := []memEntry{
		{id: "a", memType: memory.MemoryTypeUser, createdAt: time.Now()},
		{id: "b", memType: memory.MemoryTypeUser, createdAt: time.Now()},
	}
	embeddings := map[string][]float32{
		"a": {1, 0, 0},
		"b": {0, 1, 0}, // 直交 = 距離1.0 > 閾値0.3
	}
	groups := buildSimilarityGroups(entries, embeddings, 0.3, 8)
	if len(groups) != 0 {
		t.Errorf("遠いベクトルではグループなし, got %d groups", len(groups))
	}
}

func TestBuildSimilarityGroups_SingleGroup(t *testing.T) {
	entries := []memEntry{
		{id: "a", memType: memory.MemoryTypeUser, createdAt: time.Now()},
		{id: "b", memType: memory.MemoryTypeUser, createdAt: time.Now()},
	}
	embeddings := map[string][]float32{
		"a": {1, 0, 0},
		"b": {0.99, 0.1, 0}, // 非常に近い
	}
	groups := buildSimilarityGroups(entries, embeddings, 0.3, 8)
	if len(groups) != 1 {
		t.Fatalf("1グループ期待, got %d", len(groups))
	}
	if len(groups[0].members) != 2 {
		t.Errorf("2メンバー期待, got %d", len(groups[0].members))
	}
}

func TestBuildSimilarityGroups_SameTypeOnly(t *testing.T) {
	entries := []memEntry{
		{id: "a", memType: memory.MemoryTypeUser, createdAt: time.Now()},
		{id: "b", memType: memory.MemoryTypeWorld, createdAt: time.Now()}, // 異なる型
	}
	embeddings := map[string][]float32{
		"a": {1, 0, 0},
		"b": {1, 0, 0}, // 同一ベクトルだが型が違う
	}
	groups := buildSimilarityGroups(entries, embeddings, 0.3, 8)
	if len(groups) != 0 {
		t.Errorf("異なる型はグルーピングしない, got %d groups", len(groups))
	}
}

func TestBuildSimilarityGroups_MaxGroupSize(t *testing.T) {
	entries := make([]memEntry, 5)
	embeddings := make(map[string][]float32)
	for i := range entries {
		id := string(rune('a' + i))
		entries[i] = memEntry{id: id, memType: memory.MemoryTypeUser, createdAt: time.Now().Add(time.Duration(i) * time.Minute)}
		embeddings[id] = []float32{1, 0, 0} // 全て同一ベクトル
	}
	groups := buildSimilarityGroups(entries, embeddings, 0.3, 3)
	if len(groups) != 1 {
		t.Fatalf("1グループ期待, got %d", len(groups))
	}
	if len(groups[0].members) != 3 {
		t.Errorf("maxGroupSize=3 で制限されるべき, got %d", len(groups[0].members))
	}
}
