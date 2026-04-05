// migrate_sqlite_to_pg は SQLite から ParadeDB (PostgreSQL) へデータを移行する。
//
// 使い方:
//
//	go run scripts/migrate_sqlite_to_pg.go \
//	  -sqlite /data/memory.db \
//	  -pg "postgres://suzuha:suzuha@suzuha-db:5432/suzuha?sslmode=disable"
package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pgvector/pgvector-go"
)

func init() {
	sqlite_vec.Auto()
}

func main() {
	sqlitePath := flag.String("sqlite", "", "SQLite database path")
	pgDSN := flag.String("pg", "", "PostgreSQL DSN")
	flag.Parse()

	if *sqlitePath == "" || *pgDSN == "" {
		flag.Usage()
		os.Exit(1)
	}

	if err := run(*sqlitePath, *pgDSN); err != nil {
		log.Fatalf("移行失敗: %v", err)
	}
	log.Println("移行完了")
}

func run(sqlitePath, pgDSN string) error {
	src, err := sql.Open("sqlite3", sqlitePath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("SQLite open: %w", err)
	}
	defer src.Close()

	dst, err := sql.Open("pgx", pgDSN)
	if err != nil {
		return fmt.Errorf("PostgreSQL open: %w", err)
	}
	defer dst.Close()

	ctx := context.Background()

	// FK 依存順にテーブルを移行
	tables := []tableSpec{
		{name: "users", columns: "id, display_name, role, affinity, metadata, is_bot, closeness, trust, interest, created_at, updated_at",
			boolCols: map[int]bool{5: true}},
		{name: "platform_links", columns: "id, user_id, platform, platform_user_id, platform_name, created_at"},
		{name: "affinity_events", columns: "id, user_id, delta, reason, axis, interaction_ids, group_start, group_end, created_at"},
		{name: "guilds", columns: "id, name, updated_at"},
		{name: "user_guild_channels", columns: "user_id, guild_id, channel_id, channel_name, last_seen_at"},
		{name: "channel_settings", columns: "channel_id, guild_id, mode, home, updated_at",
			boolCols: map[int]bool{3: true}},
		{name: "channel_activity", columns: "channel_id, last_user_message_at"},
		{name: "channel_summaries", columns: "channel_id, channel_name, guild_id, guild_name, is_dm, user_id, summary, last_active, updated_at",
			boolCols: map[int]bool{4: true}},
		{name: "memories", columns: "id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at"},
		{name: "conversation_logs", columns: "turn_id, channel_id, role, content, user_id, user_name, message_id, tool_calls, tool_call_id, source_key, timestamp",
			skipID: true},
		{name: "context_snapshot", columns: "source_key, messages, updated_at"},
		{name: "task_state", columns: "task_name, state, updated_at"},
		{name: "scheduled_actions", columns: "id, channel_id, content, scheduled_at, cron_expr, random_minutes, created_by, mode, status, retry_count, executed_at, created_at"},
		{name: "diary_entries", columns: "id, kind, content, period_start, period_end, metadata, created_at"},
		{name: "locations", columns: "id, device_id, latitude, longitude, altitude, speed, horizontal_accuracy, battery_level, battery_state, motion, wifi, address, timestamp, created_at"},
		{name: "location_devices", columns: "device_id, owner_name, user_id, created_at"},
		{name: "location_places", columns: "id, name, latitude, longitude, radius_m, created_at"},
		{name: "app_settings", columns: "key, value"},
		{name: "preferences", columns: "category, topic, stance, confidence, reasoning, encounters, shared, last_evaluated_at, created_at, updated_at",
			skipID: true, boolCols: map[int]bool{6: true}},
		{name: "mcp_apps", columns: "name, title, description, version, registry_type, identifier, command, args, env, transport, installed_at, enabled",
			boolCols: map[int]bool{11: true}},
		{name: "llm_providers", columns: "name, type, api_key, api_base, source, created_at, updated_at"},
		{name: "llm_model_catalog", columns: "provider_name, model_id, capabilities, max_context, source, created_at"},
		{name: "llm_role_assignments", columns: "role, preset, provider_name, model_id"},
		{name: "rss_feeds", columns: "id, name, url, channel_id, created_by, enabled, last_polled, created_at, updated_at",
			boolCols: map[int]bool{5: true}},
		{name: "rss_items", columns: "id, feed_id, guid, title, link, description, published_at, memory_id, notified, created_at",
			boolCols: map[int]bool{8: true}},
	}

	// FK チェックを一時無効化 (孤立データがある場合)
	dst.ExecContext(ctx, "SET session_replication_role = 'replica'")
	defer dst.ExecContext(ctx, "SET session_replication_role = 'origin'")

	for _, t := range tables {
		n, err := migrateTable(ctx, src, dst, t)
		if err != nil {
			return fmt.Errorf("テーブル %s: %w", t.name, err)
		}
		log.Printf("  %s: %d 行", t.name, n)
	}

	// ベクトルデータ移行
	n, err := migrateVectors(ctx, src, dst)
	if err != nil {
		return fmt.Errorf("ベクトル移行: %w", err)
	}
	log.Printf("  memories (embedding): %d 件", n)

	return nil
}

type tableSpec struct {
	name     string
	columns  string
	skipID   bool         // SERIAL PK のテーブル (id 列をスキップ)
	boolCols map[int]bool // INTEGER → BOOLEAN に変換する列インデックス
}

func migrateTable(ctx context.Context, src, dst *sql.DB, spec tableSpec) (int, error) {
	cols := strings.Split(spec.columns, ", ")
	query := fmt.Sprintf("SELECT %s FROM %s", spec.columns, spec.name)

	rows, err := src.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("SELECT: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return count, fmt.Errorf("scan: %w", err)
		}

		// 不正 UTF-8 バイトを除去
		for i, v := range vals {
			if s, ok := v.(string); ok && !utf8.ValidString(s) {
				vals[i] = strings.ToValidUTF8(s, "")
			}
		}

		// INTEGER → BOOLEAN 変換
		for idx := range spec.boolCols {
			if idx < len(vals) {
				switch v := vals[idx].(type) {
				case int64:
					vals[idx] = v != 0
				case float64:
					vals[idx] = v != 0
				}
			}
		}

		placeholders := make([]string, len(cols))
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}

		insertQ := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
			spec.name, spec.columns, strings.Join(placeholders, ", "))

		if _, err := dst.ExecContext(ctx, insertQ, vals...); err != nil {
			return count, fmt.Errorf("INSERT %s: %w (vals=%v)", spec.name, err, vals)
		}
		count++
	}
	return count, rows.Err()
}

func migrateVectors(ctx context.Context, src, dst *sql.DB) (int, error) {
	// sqlite-vec API 経由でベクトルを読み取る
	rows, err := src.QueryContext(ctx,
		`SELECT id, embedding FROM memories_vec`)
	if err != nil {
		return 0, fmt.Errorf("SELECT vec: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return count, fmt.Errorf("scan vec: %w", err)
		}

		vec := deserializeFloat32Vec(blob)
		if len(vec) == 0 || len(vec) > 16000 {
			log.Printf("  スキップ: id=%s dims=%d", id, len(vec))
			continue
		}

		_, err := dst.ExecContext(ctx,
			`UPDATE memories SET embedding = $1 WHERE id = $2`,
			pgvector.NewVector(vec), id)
		if err != nil {
			log.Printf("  警告: embedding 更新失敗 id=%s dims=%d: %v", id, len(vec), err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

func deserializeFloat32Vec(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(blob)/4)
	for i := range vec {
		bits := binary.LittleEndian.Uint32(blob[i*4 : (i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}
