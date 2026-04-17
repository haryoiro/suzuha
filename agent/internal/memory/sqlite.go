//go:build sqlite

package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/haryoiro/suzuha/external/embedding"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

var sqliteVecInit sync.Once

// memColumns は memories テーブルの SELECT クエリ用の正規カラムリスト。
const memColumns = "id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at"

// memColumnsQualified は指定されたテーブルエイリアス（例: "m"）をプレフィックスに付けた memColumns を返す。
func memColumnsQualified(alias string) string {
	cols := []string{"id", "type", "content", "metadata", "keywords", "topic", "persons", "event_time", "created_at", "updated_at"}
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// scanMem は memColumns の順序に合致する単一のメモリ行をスキャンする。
func scanMem(scanner interface{ Scan(dest ...any) error }) (Memory, error) {
	var m Memory
	var typeStr string
	var metaJSON string
	var keywordsStr, topicStr, personsStr sql.NullString
	var eventTime sql.NullTime

	if err := scanner.Scan(
		&m.ID, &typeStr, &m.Content, &metaJSON,
		&keywordsStr, &topicStr, &personsStr, &eventTime,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return m, err
	}

	m.Type = MemoryType(typeStr)
	if metaJSON != "" {
		if err := json.Unmarshal([]byte(metaJSON), &m.Metadata); err != nil {
			slog.Warn("memory: メタデータのJSON解析に失敗", "id", m.ID, "error", err)
		}
	}
	if keywordsStr.Valid {
		if err := json.Unmarshal([]byte(keywordsStr.String), &m.Keywords); err != nil {
			slog.Warn("memory: キーワードのJSON解析に失敗", "id", m.ID, "error", err)
		}
	}
	if topicStr.Valid {
		m.Topic = topicStr.String
	}
	if personsStr.Valid {
		if err := json.Unmarshal([]byte(personsStr.String), &m.Persons); err != nil {
			slog.Warn("memory: 人物情報のJSON解析に失敗", "id", m.ID, "error", err)
		}
	}
	if eventTime.Valid {
		t := eventTime.Time
		m.EventTime = &t
	}
	unpackAttachments(&m)
	return m, nil
}

// marshalStringSlice は文字列スライスをJSONエンコードした文字列を返す。空の場合は nil を返す。
func marshalStringSlice(s []string) (any, error) {
	if len(s) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("memory: JSONエンコードに失敗: %w", err)
	}
	return string(b), nil
}

// nullTimePtr は *time.Time から sql.NullTime を返す。
func nullTimePtr(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// nullString はSQL挿入用に *string（空の場合は nil）を返す。
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SQLiteStore implements Store using SQLite + sqlite-vec + FTS5.
type SQLiteStore struct {
	db         *sql.DB
	embedder   embedding.Embedder
	mediaStore MediaStore
	onSave     func() // optional hook called on successful Save
	logger     *slog.Logger
	embedSig   chan struct{} // signals the background embedding worker
}

// SetMediaStore sets the media store for loading attachments during embedding.
func (s *SQLiteStore) SetMediaStore(ms MediaStore) { s.mediaStore = ms }

// NewSQLiteStore opens or creates a SQLite database at dbPath.
// If runMigrations is true, pending schema migrations are applied.
// Typically only the agent process should run migrations to avoid race conditions.
func NewSQLiteStore(dbPath string, embedder embedding.Embedder, runMigrations bool, logger *slog.Logger) (*SQLiteStore, error) {
	sqliteVecInit.Do(sqlite_vec.Auto)
	if logger == nil {
		logger = slog.Default()
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("memory: DB接続に失敗: %w", err)
	}

	// Enable WAL mode for concurrent reads.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: WALモードの設定に失敗: %w", err)
	}

	if runMigrations {
		if err := migrate(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: マイグレーションに失敗: %w", err)
		}
	}

	return &SQLiteStore{db: db, embedder: embedder, logger: logger, embedSig: make(chan struct{}, 1)}, nil
}

// SetOnSave registers a callback invoked after each successful Save.
func (s *SQLiteStore) SetOnSave(fn func()) { s.onSave = fn }

// DB returns the underlying *sql.DB for sharing with other stores (e.g. user.Store).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
