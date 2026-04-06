//go:build sqlite

package diary

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/haryoiro/suzuha/internal/scheduler"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE conversation_logs (
		source_key TEXT, channel_id TEXT, role TEXT, user_name TEXT,
		content TEXT, timestamp TEXT, message_id TEXT, turn_id TEXT,
		user_id TEXT, tool_calls TEXT, tool_call_id TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testCronContext(db *sql.DB) *scheduler.CronContext {
	return &scheduler.CronContext{
		DB:     db,
		Logger: slog.Default(),
	}
}

func insertLog(t *testing.T, db *sql.DB, role, userName, content string, ts time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO conversation_logs (source_key, channel_id, role, user_name, content, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		"discord", "ch1", role, userName, content, ts,
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFetchConversationLogs_TimezoneConsistency verifies that the query
// correctly matches timestamps regardless of timezone format.
// This was the root cause of the "only 8:00 digest" bug: timestamps stored
// in JST (+09:00) didn't match UTC (Z) query parameters in SQLite string comparison.
func TestFetchConversationLogs_TimezoneConsistency(t *testing.T) {
	db := setupTestDB(t)
	jst := time.FixedZone("JST", 9*60*60)

	// Insert logs at various hours in JST (mimics how the agent stores them).
	hours := []int{8, 9, 10, 14, 20, 23}
	for _, h := range hours {
		ts := time.Date(2026, 3, 29, h, 30, 0, 0, jst)
		insertLog(t, db, "user", "tester", fmt.Sprintf("msg at %d:30", h), ts)
		insertLog(t, db, "assistant", "", fmt.Sprintf("reply at %d:30", h), ts.Add(time.Second))
	}

	cc := testCronContext(db)

	for _, h := range hours {
		windowStart := time.Date(2026, 3, 29, h, 0, 0, 0, jst)
		windowEnd := time.Date(2026, 3, 29, h+1, 0, 0, 0, jst)

		logs := fetchConversationLogs(context.Background(), cc, windowStart, windowEnd)
		if len(logs) != 2 {
			t.Errorf("hour %d: expected 2 logs, got %d", h, len(logs))
		}
	}
}

// TestFetchConversationLogs_WindowBoundaries verifies messages exactly at
// window boundaries are included/excluded correctly.
func TestFetchConversationLogs_WindowBoundaries(t *testing.T) {
	db := setupTestDB(t)
	jst := time.FixedZone("JST", 9*60*60)

	base := time.Date(2026, 3, 29, 10, 0, 0, 0, jst)

	// Exactly at window start (inclusive).
	insertLog(t, db, "user", "a", "at start", base)
	// Middle of window.
	insertLog(t, db, "user", "b", "middle", base.Add(30*time.Minute))
	// Exactly at window end (exclusive).
	insertLog(t, db, "user", "c", "at end", base.Add(time.Hour))
	// After window.
	insertLog(t, db, "user", "d", "after", base.Add(time.Hour+time.Minute))

	cc := testCronContext(db)
	logs := fetchConversationLogs(context.Background(), cc, base, base.Add(time.Hour))

	if len(logs) != 2 {
		t.Errorf("expected 2 logs (start+middle), got %d", len(logs))
		for _, l := range logs {
			t.Logf("  %s: %s", l.UserName, l.Content)
		}
	}
}

// TestFetchConversationLogs_EmptyWindow returns nil for an hour with no activity.
func TestFetchConversationLogs_EmptyWindow(t *testing.T) {
	db := setupTestDB(t)
	jst := time.FixedZone("JST", 9*60*60)

	// Insert at 10:30 but query 11:00-12:00.
	insertLog(t, db, "user", "a", "msg", time.Date(2026, 3, 29, 10, 30, 0, 0, jst))

	cc := testCronContext(db)
	windowStart := time.Date(2026, 3, 29, 11, 0, 0, 0, jst)
	windowEnd := time.Date(2026, 3, 29, 12, 0, 0, 0, jst)

	logs := fetchConversationLogs(context.Background(), cc, windowStart, windowEnd)
	if len(logs) != 0 {
		t.Errorf("expected 0 logs for empty window, got %d", len(logs))
	}
}

// TestFetchConversationLogs_FiltersRoles verifies only user/assistant roles are returned.
func TestFetchConversationLogs_FiltersRoles(t *testing.T) {
	db := setupTestDB(t)
	jst := time.FixedZone("JST", 9*60*60)
	ts := time.Date(2026, 3, 29, 10, 30, 0, 0, jst)

	insertLog(t, db, "user", "a", "user msg", ts)
	insertLog(t, db, "assistant", "", "bot msg", ts.Add(time.Second))
	insertLog(t, db, "system", "", "system msg", ts.Add(2*time.Second))
	insertLog(t, db, "tool", "", "tool msg", ts.Add(3*time.Second))

	cc := testCronContext(db)
	windowStart := time.Date(2026, 3, 29, 10, 0, 0, 0, jst)
	windowEnd := time.Date(2026, 3, 29, 11, 0, 0, 0, jst)

	logs := fetchConversationLogs(context.Background(), cc, windowStart, windowEnd)
	if len(logs) != 2 {
		t.Errorf("expected 2 logs (user+assistant), got %d", len(logs))
	}
}
