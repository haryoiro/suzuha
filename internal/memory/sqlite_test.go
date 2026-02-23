package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewSQLiteStore(dbPath, nil, true)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		os.Remove(dbPath)
	})
	return store
}

func TestSaveAndSearch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Save a user memory.
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

	// ID should be auto-generated.
	if mem.ID == "" {
		t.Fatal("expected ID to be set after Save")
	}

	// Search should find it.
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
	if results[0].Type != MemoryTypeUser {
		t.Errorf("unexpected type: %s", results[0].Type)
	}
}

func TestSearchByType(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Save memories of different types.
	store.Save(ctx, &Memory{Type: MemoryTypeUser, Content: "ユーザーは猫が好き"})
	store.Save(ctx, &Memory{Type: MemoryTypeWorld, Content: "猫は哺乳類"})

	// Search by user type.
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

func TestSearchNoResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	results, err := store.Search(ctx, "存在しないキーワード", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSaveAutoGeneratesTimestamps(t *testing.T) {
	store := newTestStore(t)
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

func TestSaveWithEmbedFunc(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	called := false
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		called = true
		return make([]float32, 768), nil
	}

	store, err := NewSQLiteStore(dbPath, embedFn, true)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	store.Save(ctx, &Memory{Type: MemoryTypeUser, Content: "embed test"})

	if !called {
		t.Error("expected embedFn to be called")
	}
}

func TestMigrateSkip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open with migrations.
	store1, err := NewSQLiteStore(dbPath, nil, true)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	store1.Close()

	// Second open without migrations should work.
	store2, err := NewSQLiteStore(dbPath, nil, false)
	if err != nil {
		t.Fatalf("second open without migrate: %v", err)
	}
	defer store2.Close()

	// Should still be able to query.
	ctx := context.Background()
	_, err = store2.Search(ctx, "test", 10)
	if err != nil {
		t.Fatalf("Search after skip migrate: %v", err)
	}
}
