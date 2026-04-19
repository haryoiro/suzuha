//go:build sqlite

package diary

import (
	"context"
	"testing"
	"time"
)

func setupDiaryTestDB(t *testing.T) *Store {
	t.Helper()
	db := setupTestDB(t)
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS diary_entries (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		content TEXT NOT NULL,
		period_start TIMESTAMP NOT NULL,
		period_end TIMESTAMP NOT NULL,
		created_at TIMESTAMP NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

func TestStore_Save(t *testing.T) {
	tests := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{
			"save hourly entry with auto ID",
			Entry{
				Kind:        "hourly",
				Content:     "午前の会話まとめ",
				PeriodStart: time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC),
				PeriodEnd:   time.Date(2026, 4, 10, 11, 0, 0, 0, time.UTC),
			},
			false,
		},
		{
			"save daily entry with explicit ID",
			Entry{
				ID:          "custom-id-123",
				Kind:        "daily",
				Content:     "一日のまとめ",
				PeriodStart: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
				PeriodEnd:   time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC),
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := setupDiaryTestDB(t)
			ctx := context.Background()

			err := store.Save(ctx, &tt.entry)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Save error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.entry.ID == "" {
				t.Error("expected non-empty ID after save")
			}
		})
	}
}

func TestStore_ListByKind(t *testing.T) {
	base := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		entries   []Entry
		kind      string
		since     time.Time
		limit     int
		wantCount int
	}{
		{
			"empty store returns empty",
			nil,
			"hourly",
			base,
			10,
			0,
		},
		{
			"filter by hourly kind",
			[]Entry{
				{Kind: "hourly", Content: "h1", PeriodStart: base.Add(time.Hour), PeriodEnd: base.Add(2 * time.Hour)},
				{Kind: "daily", Content: "d1", PeriodStart: base, PeriodEnd: base.Add(24 * time.Hour)},
				{Kind: "hourly", Content: "h2", PeriodStart: base.Add(2 * time.Hour), PeriodEnd: base.Add(3 * time.Hour)},
			},
			"hourly",
			base,
			10,
			2,
		},
		{
			"all kinds when kind is empty",
			[]Entry{
				{Kind: "hourly", Content: "h1", PeriodStart: base.Add(time.Hour), PeriodEnd: base.Add(2 * time.Hour)},
				{Kind: "daily", Content: "d1", PeriodStart: base, PeriodEnd: base.Add(24 * time.Hour)},
			},
			"",
			base,
			10,
			2,
		},
		{
			"since filter excludes old entries",
			[]Entry{
				{Kind: "hourly", Content: "old", PeriodStart: base.Add(-24 * time.Hour), PeriodEnd: base.Add(-23 * time.Hour)},
				{Kind: "hourly", Content: "new", PeriodStart: base.Add(time.Hour), PeriodEnd: base.Add(2 * time.Hour)},
			},
			"hourly",
			base,
			10,
			1,
		},
		{
			"limit caps results",
			[]Entry{
				{Kind: "hourly", Content: "h1", PeriodStart: base.Add(time.Hour), PeriodEnd: base.Add(2 * time.Hour)},
				{Kind: "hourly", Content: "h2", PeriodStart: base.Add(2 * time.Hour), PeriodEnd: base.Add(3 * time.Hour)},
				{Kind: "hourly", Content: "h3", PeriodStart: base.Add(3 * time.Hour), PeriodEnd: base.Add(4 * time.Hour)},
			},
			"hourly",
			base,
			2,
			2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := setupDiaryTestDB(t)
			ctx := context.Background()

			for i := range tt.entries {
				if err := store.Save(ctx, &tt.entries[i]); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}

			entries, err := store.ListByKind(ctx, tt.kind, tt.since, tt.limit)
			if err != nil {
				t.Fatalf("ListByKind: %v", err)
			}
			if len(entries) != tt.wantCount {
				t.Errorf("count = %d, want %d", len(entries), tt.wantCount)
			}
		})
	}
}

func TestStore_ListByKind_OrderDesc(t *testing.T) {
	store := setupDiaryTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	entries := []Entry{
		{Kind: "hourly", Content: "first", PeriodStart: base.Add(time.Hour), PeriodEnd: base.Add(2 * time.Hour)},
		{Kind: "hourly", Content: "second", PeriodStart: base.Add(3 * time.Hour), PeriodEnd: base.Add(4 * time.Hour)},
		{Kind: "hourly", Content: "third", PeriodStart: base.Add(2 * time.Hour), PeriodEnd: base.Add(3 * time.Hour)},
	}
	for i := range entries {
		if err := store.Save(ctx, &entries[i]); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	result, err := store.ListByKind(ctx, "hourly", base, 10)
	if err != nil {
		t.Fatalf("ListByKind: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("count = %d, want 3", len(result))
	}
	if result[0].Content != "second" {
		t.Errorf("first result Content = %q, want %q (descending order)", result[0].Content, "second")
	}
}
