package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/external/embedding"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pgvector/pgvector-go"
)

// PostgresStore は ParadeDB (PostgreSQL + pgvector + pg_search) を使った Store 実装。
type PostgresStore struct {
	db         *sql.DB
	clock      *jtime.Clock
	embedder   embedding.Embedder
	mediaStore MediaStore
	onSave     func()
	logger     *slog.Logger
	embedSig   chan struct{}
}

// NewPostgresStore は ParadeDB に接続し、マイグレーションを実行する。
func NewPostgresStore(dsn string, clock *jtime.Clock, embedder embedding.Embedder, runMigrations bool, logger *slog.Logger) (*PostgresStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory: PostgreSQL 接続に失敗: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: PostgreSQL ping に失敗: %w", err)
	}

	if runMigrations {
		if err := migratePostgres(db); err != nil {
			db.Close()
			return nil, err
		}
	}

	return &PostgresStore{
		db:       db,
		clock:    clock,
		embedder: embedder,
		logger:   logger,
		embedSig: make(chan struct{}, 1),
	}, nil
}

func (s *PostgresStore) SetMediaStore(ms MediaStore) { s.mediaStore = ms }
func (s *PostgresStore) SetOnSave(fn func())         { s.onSave = fn }

func (s *PostgresStore) DB() *sql.DB { return s.db }

func (s *PostgresStore) Close() error { return s.db.Close() }

// truncateAll はテスト用に全データを削除する。
func (s *PostgresStore) truncateAll(ctx context.Context) {
	s.db.ExecContext(ctx, "TRUNCATE memories CASCADE")
}

const pgMemColumns = "id, type, content, embedding, metadata, keywords, topic, persons, event_time, created_at, updated_at"

func scanPGMem(row interface{ Scan(dest ...any) error }) (Memory, error) {
	var m Memory
	var metaJSON, keywordsJSON, topicStr, personsJSON sql.NullString
	var eventTime sql.NullTime
	var embBytes []byte

	err := row.Scan(
		&m.ID, &m.Type, &m.Content, &embBytes,
		&metaJSON, &keywordsJSON, &topicStr, &personsJSON,
		&eventTime, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return m, err
	}

	if metaJSON.Valid && metaJSON.String != "" {
		json.Unmarshal([]byte(metaJSON.String), &m.Metadata)
		if atts, ok := m.Metadata["attachments"]; ok {
			if raw, err := json.Marshal(atts); err == nil {
				json.Unmarshal(raw, &m.Attachments)
			}
		}
	}
	if keywordsJSON.Valid && keywordsJSON.String != "" {
		json.Unmarshal([]byte(keywordsJSON.String), &m.Keywords)
	}
	if topicStr.Valid {
		m.Topic = topicStr.String
	}
	if personsJSON.Valid && personsJSON.String != "" {
		json.Unmarshal([]byte(personsJSON.String), &m.Persons)
	}
	if eventTime.Valid {
		m.EventTime = &eventTime.Time
	}

	return m, nil
}

func (s *PostgresStore) initMemFields(mem *Memory) {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := s.clock.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now
}

func jsonOrNull(v any) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(data), Valid: true}
}

func (s *PostgresStore) Save(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	meta := mem.Metadata
	if len(mem.Attachments) > 0 {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["attachments"] = mem.Attachments
	}

	var embValue any
	if len(mem.Embedding) > 0 {
		embValue = pgvector.NewVector(mem.Embedding)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, type, content, embedding, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type, content = EXCLUDED.content, embedding = EXCLUDED.embedding,
		   metadata = EXCLUDED.metadata, keywords = EXCLUDED.keywords, topic = EXCLUDED.topic,
		   persons = EXCLUDED.persons, event_time = EXCLUDED.event_time, updated_at = EXCLUDED.updated_at`,
		mem.ID, mem.Type, mem.Content, embValue,
		jsonOrNull(meta), jsonOrNull(mem.Keywords), mem.Topic, jsonOrNull(mem.Persons),
		mem.EventTime, mem.CreatedAt, mem.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("memory: save に失敗: %w", err)
	}

	if s.onSave != nil {
		s.onSave()
	}
	if len(mem.Embedding) == 0 {
		s.notifyEmbedWorker()
	}
	return nil
}

func (s *PostgresStore) notifyEmbedWorker() {
	select {
	case s.embedSig <- struct{}{}:
	default:
	}
}

// SaveRaw は embedding 処理なしでメモリを保存する。
func (s *PostgresStore) SaveRaw(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)
	meta := mem.Metadata
	if len(mem.Attachments) > 0 {
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["attachments"] = mem.Attachments
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO UPDATE SET
		   type = EXCLUDED.type, content = EXCLUDED.content,
		   metadata = EXCLUDED.metadata, keywords = EXCLUDED.keywords, topic = EXCLUDED.topic,
		   persons = EXCLUDED.persons, event_time = EXCLUDED.event_time, updated_at = EXCLUDED.updated_at`,
		mem.ID, mem.Type, mem.Content,
		jsonOrNull(meta), jsonOrNull(mem.Keywords), mem.Topic, jsonOrNull(mem.Persons),
		mem.EventTime, mem.CreatedAt, mem.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) ListByUser(ctx context.Context, userID string, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = 'user' AND metadata->>'user_id' = $1
		 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, userID, limit)
}

func (s *PostgresStore) ListEpisodesByParticipant(ctx context.Context, userID string, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = 'episode' AND persons ? $1
		 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, userID, limit)
}

func (s *PostgresStore) ListByType(ctx context.Context, memType MemoryType, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = $1 ORDER BY updated_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, string(memType), limit)
}

func (s *PostgresStore) ListRecentByType(ctx context.Context, memType MemoryType, since time.Time, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE type = $1 AND created_at >= $2
		 ORDER BY created_at DESC LIMIT $3`, pgMemColumns)
	return s.queryMems(ctx, q, string(memType), since, limit)
}

func (s *PostgresStore) ListRecent(ctx context.Context, since time.Time, limit int) ([]Memory, error) {
	q := fmt.Sprintf(
		`SELECT %s FROM memories WHERE created_at >= $1
		 ORDER BY created_at DESC LIMIT $2`, pgMemColumns)
	return s.queryMems(ctx, q, since, limit)
}

func (s *PostgresStore) queryMems(ctx context.Context, query string, args ...any) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Memory
	for rows.Next() {
		m, err := scanPGMem(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, m)
	}
	return results, rows.Err()
}
