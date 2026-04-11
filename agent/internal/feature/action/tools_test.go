//go:build sqlite

package action

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

)

// setupActionTestDB is defined in store_test.go

func TestCreateTool_Execute(t *testing.T) {
	// scheduled_at を未来の固定時刻で生成する
	futureTime := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)

	tests := []struct {
		name      string
		input     map[string]any
		wantError bool
		wantMsg   string
	}{
		{
			"valid one-shot schedule",
			map[string]any{
				"channel_id":   "ch1",
				"content":      "hello",
				"scheduled_at": futureTime,
			},
			false,
			"スケジュール登録完了",
		},
		{
			"missing channel_id",
			map[string]any{
				"content":      "hello",
				"scheduled_at": futureTime,
			},
			true,
			"channel_id は必須です",
		},
		{
			"empty content",
			map[string]any{
				"channel_id":   "ch1",
				"content":      "   ",
				"scheduled_at": futureTime,
			},
			true,
			"content は必須です",
		},
		{
			"content too long",
			map[string]any{
				"channel_id":   "ch1",
				"content":      strings.Repeat("あ", 2001),
				"scheduled_at": futureTime,
			},
			true,
			"content は2000文字以下",
		},
		{
			"invalid cron expression",
			map[string]any{
				"channel_id": "ch1",
				"content":    "hello",
				"cron_expr":  "invalid",
			},
			true,
			"無効な cron_expr",
		},
		{
			"invalid scheduled_at format",
			map[string]any{
				"channel_id":   "ch1",
				"content":      "hello",
				"scheduled_at": "2025/01/15 10:00",
			},
			true,
			"scheduled_at はRFC3339形式",
		},
		{
			"neither scheduled_at nor cron_expr",
			map[string]any{
				"channel_id": "ch1",
				"content":    "hello",
			},
			true,
			"scheduled_at または cron_expr のいずれかが必須",
		},
		{
			"valid cron expression auto-calculates scheduled_at",
			map[string]any{
				"channel_id": "ch1",
				"content":    "recurring msg",
				"cron_expr":  "0 8 * * *",
			},
			false,
			"スケジュール登録完了",
		},
		{
			"valid with mode prompt",
			map[string]any{
				"channel_id":   "ch1",
				"content":      "何か面白いことを言って",
				"scheduled_at": futureTime,
				"mode":         "prompt",
			},
			false,
			"スケジュール登録完了",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			tool := NewCreateTool(store)

			inputJSON, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatal(err)
			}

			result, err := tool.Execute(context.Background(), inputJSON)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v; msg = %s", result.IsError, tt.wantError, result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, tt.wantMsg) {
				t.Errorf("result text = %q, want to contain %q", result.Content[0].Text, tt.wantMsg)
			}
		})
	}
}

func TestCancelTool_Execute(t *testing.T) {
	tests := []struct {
		name      string
		setupFn   func(*testing.T, *Store) string
		inputID   string
		wantError bool
		wantMsg   string
	}{
		{
			"cancel existing pending action",
			func(t *testing.T, s *Store) string {
				t.Helper()
				a := &Action{
					ChannelID:   "ch1",
					Content:     "test",
					ScheduledAt: time.Now().Add(time.Hour),
				}
				if err := s.Create(context.Background(), a); err != nil {
					t.Fatal(err)
				}
				return a.ID
			},
			"",
			false,
			"キャンセルしました",
		},
		{
			"cancel non-existent action",
			nil,
			"non-existent-id",
			true,
			"見つからない",
		},
		{
			"empty id",
			nil,
			"",
			true,
			"id は必須です",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			tool := NewCancelTool(store)

			id := tt.inputID
			if tt.setupFn != nil {
				id = tt.setupFn(t, store)
			}

			input, err := json.Marshal(map[string]string{"id": id})
			if err != nil {
				t.Fatal(err)
			}

			result, err := tool.Execute(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.IsError != tt.wantError {
				t.Errorf("IsError = %v, want %v; msg = %s", result.IsError, tt.wantError, result.Content[0].Text)
			}
			if !strings.Contains(result.Content[0].Text, tt.wantMsg) {
				t.Errorf("result text = %q, want to contain %q", result.Content[0].Text, tt.wantMsg)
			}
		})
	}
}

func TestListTool_Execute(t *testing.T) {
	tests := []struct {
		name      string
		setupFn   func(*testing.T, *Store)
		input     map[string]string
		wantMsg   string
	}{
		{
			"empty list",
			nil,
			nil,
			"保留中のスケジュールはありません",
		},
		{
			"list with actions",
			func(t *testing.T, s *Store) {
				t.Helper()
				a := &Action{
					ChannelID:   "ch1",
					Content:     "scheduled message",
					ScheduledAt: time.Now().Add(time.Hour),
				}
				if err := s.Create(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			},
			nil,
			"scheduled message",
		},
		{
			"filter by creator",
			func(t *testing.T, s *Store) {
				t.Helper()
				for _, creator := range []string{"user1", "user2"} {
					a := &Action{
						ChannelID:   "ch1",
						Content:     "msg from " + creator,
						ScheduledAt: time.Now().Add(time.Hour),
						CreatedBy:   creator,
					}
					if err := s.Create(context.Background(), a); err != nil {
						t.Fatal(err)
					}
				}
			},
			map[string]string{"created_by": "user1"},
			"msg from user1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupActionTestDB(t)
			store := NewStore(db)
			tool := NewListTool(store)

			if tt.setupFn != nil {
				tt.setupFn(t, store)
			}

			var inputJSON json.RawMessage
			if tt.input != nil {
				b, err := json.Marshal(tt.input)
				if err != nil {
					t.Fatal(err)
				}
				inputJSON = b
			}

			result, err := tool.Execute(context.Background(), inputJSON)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.Contains(result.Content[0].Text, tt.wantMsg) {
				t.Errorf("result text = %q, want to contain %q", result.Content[0].Text, tt.wantMsg)
			}
		})
	}
}
