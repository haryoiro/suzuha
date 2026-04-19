//go:build sqlite

package action

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupActionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?_loc=UTC")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE scheduled_actions (
		id             TEXT PRIMARY KEY,
		channel_id     TEXT NOT NULL,
		content        TEXT NOT NULL,
		mode           TEXT NOT NULL DEFAULT 'direct',
		scheduled_at   TIMESTAMP NOT NULL,
		cron_expr      TEXT,
		random_minutes INTEGER NOT NULL DEFAULT 0,
		created_by     TEXT,
		status         TEXT NOT NULL DEFAULT 'pending',
		retry_count    INTEGER NOT NULL DEFAULT 0,
		executed_at    TIMESTAMP,
		created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduled_actions_due
		ON scheduled_actions (status, scheduled_at)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStore_CreateAndListPending(t *testing.T) {
	tests := []struct {
		name      string
		actions   []Action
		wantCount int
	}{
		{
			"no actions",
			nil,
			0,
		},
		{
			"single pending action",
			[]Action{
				{ChannelID: "ch1", Content: "hello", ScheduledAt: time.Now().Add(time.Hour)},
			},
			1,
		},
		{
			"multiple pending actions",
			[]Action{
				{ChannelID: "ch1", Content: "first", ScheduledAt: time.Now().Add(time.Hour)},
				{ChannelID: "ch2", Content: "second", ScheduledAt: time.Now().Add(2 * time.Hour)},
			},
			2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			ctx := context.Background()

			for i := range tt.actions {
				if err := store.Create(ctx, &tt.actions[i]); err != nil {
					t.Fatalf("Create: %v", err)
				}
				if tt.actions[i].ID == "" {
					t.Error("expected auto-generated ID, got empty")
				}
			}

			actions, err := store.ListPending(ctx)
			if err != nil {
				t.Fatalf("ListPending: %v", err)
			}
			if len(actions) != tt.wantCount {
				t.Errorf("ListPending count = %d, want %d", len(actions), tt.wantCount)
			}
		})
	}
}

func TestStore_Cancel(t *testing.T) {
	tests := []struct {
		name      string
		setupFn   func(*testing.T, *Store) string
		cancelID  string
		wantOK    bool
	}{
		{
			"cancel pending action",
			func(t *testing.T, s *Store) string {
				t.Helper()
				a := &Action{ChannelID: "ch1", Content: "test", ScheduledAt: time.Now().Add(time.Hour)}
				if err := s.Create(context.Background(), a); err != nil {
					t.Fatal(err)
				}
				return a.ID
			},
			"",
			true,
		},
		{
			"cancel non-existent ID",
			nil,
			"does-not-exist",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			ctx := context.Background()

			id := tt.cancelID
			if tt.setupFn != nil {
				id = tt.setupFn(t, store)
			}

			ok, err := store.Cancel(ctx, id)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if ok != tt.wantOK {
				t.Errorf("Cancel ok = %v, want %v", ok, tt.wantOK)
			}

			if tt.wantOK {
				actions, err := store.ListPending(ctx)
				if err != nil {
					t.Fatalf("ListPending: %v", err)
				}
				if len(actions) != 0 {
					t.Errorf("expected 0 pending after cancel, got %d", len(actions))
				}
			}
		})
	}
}

func TestStore_FetchDue(t *testing.T) {
	tests := []struct {
		name      string
		schedules []time.Duration
		fetchAt   time.Duration
		wantCount int
	}{
		{
			"no due actions",
			[]time.Duration{2 * time.Hour},
			time.Hour,
			0,
		},
		{
			"one due action",
			[]time.Duration{-time.Hour},
			0,
			1,
		},
		{
			"mixed due and not due",
			[]time.Duration{-time.Hour, 2 * time.Hour, -30 * time.Minute},
			0,
			2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			ctx := context.Background()
			now := time.Now()

			for _, offset := range tt.schedules {
				a := &Action{
					ChannelID:   "ch1",
					Content:     "test",
					ScheduledAt: now.Add(offset),
				}
				if err := store.Create(ctx, a); err != nil {
					t.Fatalf("Create: %v", err)
				}
			}

			due, err := store.FetchDue(ctx, now.Add(tt.fetchAt))
			if err != nil {
				t.Fatalf("FetchDue: %v", err)
			}
			if len(due) != tt.wantCount {
				t.Errorf("FetchDue count = %d, want %d", len(due), tt.wantCount)
			}
		})
	}
}

func TestStore_ListPendingByCreator(t *testing.T) {
	tests := []struct {
		name      string
		creators  []string
		filterBy  string
		wantCount int
	}{
		{
			"filter returns matching actions",
			[]string{"alice", "bob", "alice"},
			"alice",
			2,
		},
		{
			"filter returns no match",
			[]string{"alice"},
			"charlie",
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			ctx := context.Background()

			for _, creator := range tt.creators {
				a := &Action{
					ChannelID:   "ch1",
					Content:     "msg from " + creator,
					ScheduledAt: time.Now().Add(time.Hour),
					CreatedBy:   creator,
				}
				if err := store.Create(ctx, a); err != nil {
					t.Fatalf("Create: %v", err)
				}
			}

			actions, err := store.ListPendingByCreator(ctx, tt.filterBy)
			if err != nil {
				t.Fatalf("ListPendingByCreator: %v", err)
			}
			if len(actions) != tt.wantCount {
				t.Errorf("count = %d, want %d", len(actions), tt.wantCount)
			}
		})
	}
}

func TestStore_MarkExecuted(t *testing.T) {
	tests := []struct {
		name            string
		cronExpr        string
		wantStillPending bool
	}{
		{
			"one-shot action marked as executed",
			"",
			false,
		},
		{
			"recurring action rescheduled",
			"0 8 * * *",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			ctx := context.Background()

			a := &Action{
				ChannelID:   "ch1",
				Content:     "test",
				ScheduledAt: time.Now().Add(-time.Hour),
				CronExpr:    tt.cronExpr,
			}
			if err := store.Create(ctx, a); err != nil {
				t.Fatalf("Create: %v", err)
			}

			if err := store.MarkExecuted(ctx, a.ID, time.Now()); err != nil {
				t.Fatalf("MarkExecuted: %v", err)
			}

			pending, err := store.ListPending(ctx)
			if err != nil {
				t.Fatalf("ListPending: %v", err)
			}

			hasPending := len(pending) > 0
			if hasPending != tt.wantStillPending {
				t.Errorf("still pending = %v, want %v", hasPending, tt.wantStillPending)
			}
		})
	}
}

func TestStore_DeleteAndUpdate(t *testing.T) {
	t.Run("delete existing action", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)
		ctx := context.Background()

		a := &Action{ChannelID: "ch1", Content: "test", ScheduledAt: time.Now().Add(time.Hour)}
		if err := store.Create(ctx, a); err != nil {
			t.Fatal(err)
		}

		if err := store.Delete(ctx, a.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		actions, err := store.ListPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 0 {
			t.Errorf("expected 0 after delete, got %d", len(actions))
		}
	})

	t.Run("delete non-existent action returns error", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)

		err := store.Delete(context.Background(), "non-existent")
		if err == nil {
			t.Error("expected error for non-existent delete, got nil")
		}
	})

	t.Run("update existing action content", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)
		ctx := context.Background()

		a := &Action{ChannelID: "ch1", Content: "original", ScheduledAt: time.Now().Add(time.Hour)}
		if err := store.Create(ctx, a); err != nil {
			t.Fatal(err)
		}

		newContent := "updated"
		if err := store.Update(ctx, a.ID, ActionUpdateFields{Content: &newContent}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		actions, err := store.ListPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 || actions[0].Content != newContent {
			t.Errorf("Content = %q, want %q", actions[0].Content, newContent)
		}
	})

	t.Run("update with no fields returns error", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)

		err := store.Update(context.Background(), "some-id", ActionUpdateFields{})
		if err == nil {
			t.Error("expected error for empty update, got nil")
		}
	})
}

func TestStore_IncrRetryAndMarkFailed(t *testing.T) {
	t.Run("increment retry count", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)
		ctx := context.Background()

		a := &Action{ChannelID: "ch1", Content: "test", ScheduledAt: time.Now().Add(time.Hour)}
		if err := store.Create(ctx, a); err != nil {
			t.Fatal(err)
		}

		if err := store.IncrRetry(ctx, a.ID, 3); err != nil {
			t.Fatalf("IncrRetry: %v", err)
		}

		actions, err := store.ListPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(actions) != 1 || actions[0].RetryCount != 3 {
			t.Errorf("RetryCount = %d, want 3", actions[0].RetryCount)
		}
	})

	t.Run("mark failed removes from pending", func(t *testing.T) {
		db := setupActionTestDB(t)
		store := NewStore(db)
		ctx := context.Background()

		a := &Action{ChannelID: "ch1", Content: "test", ScheduledAt: time.Now().Add(time.Hour)}
		if err := store.Create(ctx, a); err != nil {
			t.Fatal(err)
		}

		if err := store.MarkFailed(ctx, a.ID, 5); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}

		pending, err := store.ListPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 0 {
			t.Errorf("expected 0 pending after MarkFailed, got %d", len(pending))
		}
	})
}
