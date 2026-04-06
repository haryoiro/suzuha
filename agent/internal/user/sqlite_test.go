package user

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/haryoiro/suzuha/internal/memory"
)

func newTestStore(t *testing.T, botIDs ...string) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Use memory package to open DB and run migrations (creates all tables).
	memStore, err := memory.NewSQLiteStore(dbPath, nil, true, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		memStore.Close()
		os.Remove(dbPath)
	})

	return NewSQLiteStore(memStore.DB(), botIDs...)
}

func TestResolve_AutoCreate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	u, err := store.Resolve(ctx, "discord", "12345", "TestUser")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if u.ID == "" {
		t.Fatal("expected user ID to be set")
	}
	if u.Role != RoleMember {
		t.Errorf("expected role=member, got %s", u.Role)
	}
}

func TestResolve_CLIIsOwner(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	u, err := store.Resolve(ctx, "cli", "local", "user")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if u.Role != RoleOwner {
		t.Errorf("expected role=owner for CLI user, got %s", u.Role)
	}
}

func TestResolve_Existing(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// First resolve creates the user.
	u1, err := store.Resolve(ctx, "discord", "12345", "TestUser")
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	// Second resolve returns the same user.
	u2, err := store.Resolve(ctx, "discord", "12345", "TestUser")
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}

	if u1.ID != u2.ID {
		t.Errorf("expected same user ID, got %s and %s", u1.ID, u2.ID)
	}
}

func TestUpdateDisplayName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	u, err := store.Resolve(ctx, "discord", "12345", "TestUser")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := store.UpdateDisplayName(ctx, u.ID, "みっちゃん"); err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}

	// Get should reflect the new name.
	updated, err := store.Get(ctx, u.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.DisplayName != "みっちゃん" {
		t.Errorf("expected display_name=みっちゃん, got %s", updated.DisplayName)
	}
}

func TestResolve_BotUser(t *testing.T) {
	botID := "999888777"
	store := newTestStore(t, botID)
	ctx := context.Background()

	u, err := store.Resolve(ctx, "discord", botID, "SuzuhaBot")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !u.IsBot {
		t.Error("expected is_bot=true for bot platform user ID")
	}

	// Regular user should not be marked as bot.
	u2, err := store.Resolve(ctx, "discord", "12345", "HumanUser")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if u2.IsBot {
		t.Error("expected is_bot=false for regular user")
	}
}

func TestResolve_ExistingUserMarkedAsBot(t *testing.T) {
	botID := "999888777"
	// First, create a store WITHOUT bot ID — simulates user created before is_bot existed.
	store := newTestStore(t)
	ctx := context.Background()

	u, err := store.Resolve(ctx, "discord", botID, "SuzuhaBot")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if u.IsBot {
		t.Fatal("expected is_bot=false before AddBotID")
	}

	// Now register the bot ID (simulates startup after Discord connect).
	store.AddBotID(botID)

	// Re-resolve — should auto-fix is_bot to true.
	u2, err := store.Resolve(ctx, "discord", botID, "SuzuhaBot")
	if err != nil {
		t.Fatalf("Resolve after AddBotID: %v", err)
	}
	if !u2.IsBot {
		t.Error("expected is_bot=true after AddBotID + Resolve")
	}
	if u.ID != u2.ID {
		t.Errorf("expected same user ID, got %s and %s", u.ID, u2.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

// Ensure that the *sql.DB parameter is correctly typed.
var _ Store = (*SQLiteStore)(nil)

func init() {
	// Prevent "imported and not used" for sql package.
	_ = sql.ErrNoRows
}
