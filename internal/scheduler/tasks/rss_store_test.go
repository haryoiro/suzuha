package tasks

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestFeedStore_Setup(t *testing.T) {
	db := setupTestDB(t)
	store := NewFeedStore(db)
	if err := store.Setup(context.Background()); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestFeedStore_AddAndList(t *testing.T) {
	db := setupTestDB(t)
	store := NewFeedStore(db)
	ctx := context.Background()
	store.Setup(ctx)

	feed := &Feed{
		Name:      "Go Blog",
		URL:       "https://go.dev/blog/feed.atom",
		ChannelID: "chan123",
		CreatedBy: "user456",
	}
	if err := store.AddFeed(ctx, feed); err != nil {
		t.Fatalf("add feed: %v", err)
	}
	if feed.ID == "" {
		t.Fatal("expected ID to be set")
	}

	feeds, err := store.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Name != "Go Blog" {
		t.Errorf("name: got %q", feeds[0].Name)
	}
	if feeds[0].ChannelID != "chan123" {
		t.Errorf("channel: got %q", feeds[0].ChannelID)
	}
}

func TestFeedStore_RemoveFeed(t *testing.T) {
	db := setupTestDB(t)
	store := NewFeedStore(db)
	ctx := context.Background()
	store.Setup(ctx)

	store.AddFeed(ctx, &Feed{Name: "Test", URL: "https://example.com/feed", ChannelID: "ch"})

	if err := store.RemoveFeed(ctx, "https://example.com/feed"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	feeds, _ := store.ListAll(ctx)
	if len(feeds) != 0 {
		t.Fatalf("expected 0 feeds after remove, got %d", len(feeds))
	}
}

func TestFeedStore_ItemOperations(t *testing.T) {
	db := setupTestDB(t)
	store := NewFeedStore(db)
	ctx := context.Background()
	store.Setup(ctx)

	feed := &Feed{Name: "Test", URL: "https://example.com/feed", ChannelID: "ch"}
	store.AddFeed(ctx, feed)

	// Insert item.
	item := &Item{
		FeedID:      feed.ID,
		GUID:        "guid-001",
		Title:       "Test Article",
		Link:        "https://example.com/article",
		Description: "Description here",
	}
	if err := store.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	// Check exists.
	exists, err := store.ItemExists(ctx, feed.ID, "guid-001")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected item to exist")
	}

	notExists, _ := store.ItemExists(ctx, feed.ID, "guid-999")
	if notExists {
		t.Fatal("expected item to not exist")
	}

	// Unnotified items.
	items, err := store.UnnotifiedItems(ctx, []string{feed.ID})
	if err != nil {
		t.Fatalf("unnotified: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 unnotified, got %d", len(items))
	}
	if items[0].Title != "Test Article" {
		t.Errorf("title: got %q", items[0].Title)
	}

	// Mark notified.
	if err := store.MarkNotified(ctx, []string{items[0].ID}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	items2, _ := store.UnnotifiedItems(ctx, []string{feed.ID})
	if len(items2) != 0 {
		t.Fatalf("expected 0 unnotified after mark, got %d", len(items2))
	}
}

func TestFeedStore_DuplicateItem(t *testing.T) {
	db := setupTestDB(t)
	store := NewFeedStore(db)
	ctx := context.Background()
	store.Setup(ctx)

	feed := &Feed{Name: "Test", URL: "https://example.com/feed", ChannelID: "ch"}
	store.AddFeed(ctx, feed)

	item := &Item{FeedID: feed.ID, GUID: "dup", Title: "Article", Link: "https://example.com/a"}
	store.InsertItem(ctx, item)

	// Duplicate insert should not fail (INSERT OR IGNORE).
	item2 := &Item{FeedID: feed.ID, GUID: "dup", Title: "Article 2", Link: "https://example.com/b"}
	if err := store.InsertItem(ctx, item2); err != nil {
		t.Fatalf("duplicate insert should not fail: %v", err)
	}
}
