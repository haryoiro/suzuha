//go:build sqlite

package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/external/embedding"
)

func (s *SQLiteStore) Save(ctx context.Context, mem *Memory) error {
	// If embedding is already provided, save everything inline.
	if len(mem.Embedding) > 0 {
		return s.saveWithEmbedding(ctx, mem)
	}
	// Otherwise, save content + FTS immediately; embedding is generated
	// asynchronously by the background worker (RunEmbeddingWorker).
	if err := s.saveContentAndFTS(ctx, mem); err != nil {
		return err
	}
	s.notifyEmbedWorker()
	return nil
}

// saveWithEmbedding persists content, FTS index, and vector embedding in one transaction.
func (s *SQLiteStore) saveWithEmbedding(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	// Delete stale FTS entry if it exists (INSERT OR REPLACE on memories may change rowid).
	tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	); err != nil {
		return fmt.Errorf("memory: FTSインデックスの挿入に失敗: %w", err)
	}

	if blob, err := sqlite_vec.SerializeFloat32(mem.Embedding); err == nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
			mem.ID, blob,
		); err != nil {
			s.logger.Warn("memory: ベクトルの挿入に失敗", "id", mem.ID, "error", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if s.onSave != nil {
		s.onSave()
	}
	return nil
}

// saveContentAndFTS persists content and FTS index without generating an embedding.
// The embedding will be backfilled asynchronously by RunEmbeddingWorker.
func (s *SQLiteStore) saveContentAndFTS(ctx context.Context, mem *Memory) error {
	s.initMemFields(mem)

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO memories (id, type, content, metadata, keywords, topic, persons, event_time, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.CreatedAt, mem.UpdatedAt,
	); err != nil {
		return fmt.Errorf("memory: レコードの挿入に失敗: %w", err)
	}

	// Delete stale FTS entry if it exists (INSERT OR REPLACE on memories may change rowid).
	tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content,
	); err != nil {
		return fmt.Errorf("memory: FTSインデックスの挿入に失敗: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if s.onSave != nil {
		s.onSave()
	}
	return nil
}

// initMemFields sets ID and timestamps if not already set.
// Also packs Attachments into Metadata for JSON storage.
func (s *SQLiteStore) initMemFields(mem *Memory) {
	if mem.ID == "" {
		mem.ID = uuid.NewString()
	}
	now := time.Now()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	mem.UpdatedAt = now
	packAttachments(mem)
}

// packAttachments writes mem.Attachments into mem.Metadata["attachments"].
// If Attachments is empty, removes the key.
func packAttachments(mem *Memory) {
	if len(mem.Attachments) == 0 {
		if mem.Metadata != nil {
			delete(mem.Metadata, "attachments")
		}
		return
	}
	if mem.Metadata == nil {
		mem.Metadata = make(map[string]any)
	}
	raw := make([]map[string]string, len(mem.Attachments))
	for i, a := range mem.Attachments {
		raw[i] = map[string]string{
			"key":       a.Key,
			"modality":  a.Modality,
			"mime_type": a.MimeType,
		}
	}
	mem.Metadata["attachments"] = raw
}

// unpackAttachments extracts Attachments from Metadata["attachments"].
func unpackAttachments(mem *Memory) {
	if mem.Metadata == nil {
		return
	}
	raw, ok := mem.Metadata["attachments"]
	if !ok {
		return
	}
	arr, ok := raw.([]any)
	if !ok {
		return
	}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		a := Attachment{}
		if v, ok := m["key"].(string); ok {
			a.Key = v
		}
		if v, ok := m["modality"].(string); ok {
			a.Modality = v
		}
		if v, ok := m["mime_type"].(string); ok {
			a.MimeType = v
		}
		if a.Key != "" {
			mem.Attachments = append(mem.Attachments, a)
		}
	}
}

// notifyEmbedWorker sends a non-blocking signal to wake up the embedding worker.
func (s *SQLiteStore) notifyEmbedWorker() {
	select {
	case s.embedSig <- struct{}{}:
	default: // already signaled
	}
}

// RunEmbeddingWorker processes pending embeddings in the background.
// It wakes up on signal (after Save) or periodically. Call as a goroutine.
func (s *SQLiteStore) RunEmbeddingWorker(ctx context.Context) {
	const batchSize = 20
	const pollInterval = 30 * time.Second
	const maxBackoff = 10 * time.Minute

	backoff := time.Duration(0)

	for {
		wait := pollInterval
		if backoff > 0 {
			wait = backoff
		}

		select {
		case <-ctx.Done():
			return
		case <-s.embedSig:
		case <-time.After(wait):
		}

	drain:
		for {
			select {
			case <-s.embedSig:
			default:
				break drain
			}
		}

		for {
			n, err := s.BackfillEmbeddings(ctx, batchSize)
			if err != nil {
				s.logger.Warn("embedding worker: バックフィルでエラー発生", "error", err)
				// Exponential backoff on error.
				if backoff == 0 {
					backoff = pollInterval
				} else {
					backoff *= 2
				}
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				s.logger.Info("embedding worker: 次回リトライまで待機", "backoff", backoff)
				break
			}
			backoff = 0 // reset on success
			if n == 0 {
				break
			}
			s.logger.Info("embedding worker: バックフィル完了", "count", n)
		}
	}
}

func (s *SQLiteStore) Update(ctx context.Context, mem *Memory) error {
	mem.UpdatedAt = time.Now()
	packAttachments(mem)

	metadataJSON, err := json.Marshal(mem.Metadata)
	if err != nil {
		return fmt.Errorf("memory: メタデータのJSON変換に失敗: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE memories SET type = ?, content = ?, metadata = ?, keywords = ?, topic = ?, persons = ?, event_time = ?, updated_at = ? WHERE id = ?`,
		string(mem.Type), mem.Content, string(metadataJSON),
		marshalStringSlice(mem.Keywords), nullString(mem.Topic),
		marshalStringSlice(mem.Persons), nullTimePtr(mem.EventTime),
		mem.UpdatedAt, mem.ID,
	)
	if err != nil {
		return fmt.Errorf("memory: 更新に失敗: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		s.logger.Warn("memory: 影響行数の取得に失敗", "id", mem.ID, "error", raErr)
	}
	if n == 0 {
		return fmt.Errorf("memory: 見つかりません: %s", mem.ID)
	}

	// Rebuild FTS index for this row.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, mem.ID); err != nil {
		s.logger.Warn("memory: 更新時のFTS削除に失敗", "id", mem.ID, "error", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memories_fts (rowid, content) VALUES (
			(SELECT rowid FROM memories WHERE id = ?), ?
		)`, mem.ID, mem.Content); err != nil {
		s.logger.Warn("memory: 更新時のFTS挿入に失敗", "id", mem.ID, "error", err)
	}

	return tx.Commit()
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: トランザクション開始に失敗: %w", err)
	}
	defer tx.Rollback()

	// Delete from FTS.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_fts WHERE rowid = (SELECT rowid FROM memories WHERE id = ?)`, id); err != nil {
		s.logger.Warn("memory: FTSの削除に失敗", "id", id, "error", err)
	}
	// Delete from vec.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memories_vec WHERE id = ?`, id); err != nil {
		s.logger.Warn("memory: ベクトルの削除に失敗", "id", id, "error", err)
	}
	// Delete from main table.
	res, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("memory: 削除に失敗: %w", err)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		s.logger.Warn("memory: 削除時の影響行数の取得に失敗", "id", id, "error", raErr)
	}
	if n == 0 {
		return fmt.Errorf("memory: 見つかりません: %s", id)
	}

	return tx.Commit()
}

func (s *SQLiteStore) DeleteBatch(ctx context.Context, ids []string) (int, error) {
	var deleted int
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			s.logger.Warn("memory: 一括削除でスキップ", "id", id, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

func (s *SQLiteStore) SaveRaw(ctx context.Context, mem *Memory) error {
	// SaveRaw saves without embedding and without signaling the worker.
	// Used by admin merge endpoint where BackfillEmbeddings handles it later.
	return s.saveContentAndFTS(ctx, mem)
}

// BackfillEmbeddings generates embeddings for memories that don't have them yet.
// Uses EmbedBatch for efficient bulk processing. Returns the number of memories processed.
func (s *SQLiteStore) BackfillEmbeddings(ctx context.Context, batchSize int) (int, error) {
	if s.embedder == nil {
		return 0, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, m.content, m.metadata FROM memories m
		 WHERE m.id NOT IN (SELECT id FROM memories_vec)
		 LIMIT ?`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("memory: バックフィルクエリに失敗: %w", err)
	}
	defer rows.Close()

	type entry struct {
		id      string
		content string
		atts    []Attachment
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var metaJSON sql.NullString
		if err := rows.Scan(&e.id, &e.content, &metaJSON); err != nil {
			return 0, fmt.Errorf("memory: バックフィルのスキャンに失敗: %w", err)
		}
		// Extract attachments from metadata.
		if metaJSON.Valid && metaJSON.String != "" {
			var m Memory
			m.Metadata = make(map[string]any)
			if err := json.Unmarshal([]byte(metaJSON.String), &m.Metadata); err == nil {
				unpackAttachments(&m)
				e.atts = m.Attachments
			}
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	// Build batch inputs, including image attachments from MediaStore.
	// Filter out entries with empty content to avoid API errors.
	type indexedInput struct {
		idx   int
		parts []embedding.Part
	}
	var valid []indexedInput
	for i, e := range entries {
		if strings.TrimSpace(e.content) == "" {
			s.logger.Warn("embedding backfill: 空コンテンツをスキップ", "id", e.id)
			continue
		}
		parts := []embedding.Part{embedding.TextPart(e.content)}
		if s.mediaStore != nil {
			for _, att := range e.atts {
				if att.Modality == "image" {
					data, err := s.mediaStore.Get(ctx, att.Key)
					if err != nil {
						continue
					}
					parts = append(parts, embedding.ImagePart(data, att.MimeType))
				}
			}
		}
		valid = append(valid, indexedInput{idx: i, parts: parts})
	}
	if len(valid) == 0 {
		return 0, nil
	}

	inputs := make([][]embedding.Part, len(valid))
	for i, v := range valid {
		inputs[i] = v.parts
	}

	vectors, err := s.embedder.EmbedBatch(ctx, inputs)
	if err != nil {
		// Log which entries were in the batch for debugging.
		for _, v := range valid {
			s.logger.Warn("embedding backfill: バッチ内エントリ",
				"id", entries[v.idx].id,
				"content_len", len(entries[v.idx].content),
				"attachments", len(entries[v.idx].atts))
		}
		return 0, fmt.Errorf("memory: バッチ埋め込みに失敗: %w", err)
	}

	var count int
	for i, vec := range vectors {
		if len(vec) == 0 {
			continue
		}
		blob, err := sqlite_vec.SerializeFloat32(vec)
		if err != nil {
			continue
		}
		entryID := entries[valid[i].idx].id
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO memories_vec (id, embedding) VALUES (?, ?)`,
			entryID, blob,
		); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

