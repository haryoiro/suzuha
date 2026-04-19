package memory

import (
	"context"
	"os"
	"testing"
)

func pgDSN() string {
	if v := os.Getenv("TEST_POSTGRES_URL"); v != "" {
		return v
	}
	return "postgres://suzuha:suzuha@suzuha-db:5432/suzuha?sslmode=disable"
}

func newTestPGStore(t *testing.T) *DBStore {
	t.Helper()
	dsn := pgDSN()
	store, err := NewDBStore(dsn, nil, true, nil)
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	t.Cleanup(func() {
		store.truncateAll(context.Background())
		store.Close()
	})
	return store
}

func TestPG_SaveAndSearch(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	mem := &Memory{
		Type:    MemoryTypeUser,
		Content: "ユーザーはGoが好き",
		Metadata: map[string]any{
			"user_id": "user123",
		},
	}
	if err := store.Save(ctx, mem); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if mem.ID == "" {
		t.Fatal("expected ID to be set after Save")
	}

	results, err := store.Search(ctx, "Go", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "ユーザーはGoが好き" {
		t.Errorf("unexpected content: %s", results[0].Content)
	}
}

func TestPG_SearchByType(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	store.Save(ctx, &Memory{Type: MemoryTypeUser, Content: "ユーザーは猫が好き"})
	store.Save(ctx, &Memory{Type: MemoryTypeWorld, Content: "猫は哺乳類"})

	results, err := store.SearchByType(ctx, "猫", MemoryTypeUser, 10)
	if err != nil {
		t.Fatalf("SearchByType: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != MemoryTypeUser {
		t.Errorf("expected user type, got %s", results[0].Type)
	}
}

func TestPG_SearchNoResults(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	results, err := store.Search(ctx, "存在しないキーワード", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestPG_SaveAutoGeneratesTimestamps(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	mem := &Memory{Type: MemoryTypeUser, Content: "test"}
	store.Save(ctx, mem)

	if mem.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if mem.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestPG_Delete(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	mem := &Memory{Type: MemoryTypeUser, Content: "消されるメモリ"}
	store.Save(ctx, mem)

	err := store.Delete(ctx, mem.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get(ctx, mem.ID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestPG_ListByType(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	store.Save(ctx, &Memory{Type: MemoryTypeUser, Content: "user memo"})
	store.Save(ctx, &Memory{Type: MemoryTypeWorld, Content: "world memo"})
	store.Save(ctx, &Memory{Type: MemoryTypeUser, Content: "user memo 2"})

	results, err := store.ListByType(ctx, MemoryTypeUser, 10)
	if err != nil {
		t.Fatalf("ListByType: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Type != MemoryTypeUser {
			t.Errorf("expected user type, got %s", r.Type)
		}
	}
}

func TestPG_BM25JapaneseSearch(t *testing.T) {
	store := newTestPGStore(t)
	ctx := context.Background()

	store.Save(ctx, &Memory{Type: MemoryTypeEpisode, Content: "今日は東京タワーに行った。天気が良くて最高だった。"})
	store.Save(ctx, &Memory{Type: MemoryTypeWorld, Content: "Go言語のジェネリクスについて学んだ。"})

	results, err := store.Search(ctx, "東京タワー", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected BM25 to find 東京タワー")
	}
	if results[0].Content != "今日は東京タワーに行った。天気が良くて最高だった。" {
		t.Errorf("unexpected top result: %s", results[0].Content)
	}
}
