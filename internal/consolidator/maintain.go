package consolidator

import (
	"context"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
)

// memEntry はメンテナンス中に使用する軽量なメモリレコード。
type memEntry struct {
	id        string
	memType   memory.MemoryType
	content   string
	metadata  map[string]any
	persons   []string
	keywords  []string
	topic     string
	createdAt time.Time
}

// memoryGroup は類似メモリのクラスタ。
type memoryGroup struct {
	memType memory.MemoryType
	members []memEntry
}

// decision はグループに対するLLM判定の結果。
type decision struct {
	action        string // "keep" または "merge"
	keepID        string
	deleteIDs     []string
	mergedContent string
	groupType     memory.MemoryType
	reason        string
	sourceEntries []memEntry
}

// Maintain はメモリの重複排除と統合パイプラインを実行する。
func (s *Server) Maintain(ctx context.Context, opts MaintainOpts) (*MaintainResult, error) {
	if s.admin == nil {
		return nil, fmt.Errorf("consolidator: AdminStore が未設定、メンテナンス実行不可")
	}

	s.logger.Info("maintain: 開始",
		"similarity_threshold", opts.SimilarityThreshold,
		"dry_run", opts.DryRun)

	// フェーズ1: 埋め込みを持つ全メモリを Store 経由で読み込む。
	memories, err := s.admin.ListEmbeddedMemories(ctx)
	if err != nil {
		return nil, fmt.Errorf("maintain: エントリの読み込みに失敗: %w", err)
	}
	entries := memoriesToEntries(memories)
	if len(entries) < 2 {
		s.logger.Info("maintain: 重複排除に必要な記憶数が不足", "count", len(entries))
		return &MaintainResult{}, nil
	}
	s.logger.Info("maintain: 記憶を読み込み完了", "count", len(entries))

	// フェーズ2: 埋め込みベクトルを Store 経由で読み込む。
	embeddings, err := s.admin.ListAllEmbeddings(ctx)
	if err != nil {
		return nil, fmt.Errorf("maintain: 埋め込みの読み込みに失敗: %w", err)
	}

	// フェーズ3: Union-Find でクラスタリング。
	groups := buildSimilarityGroups(entries, embeddings, opts.SimilarityThreshold, opts.MaxGroupSize)
	if len(groups) == 0 {
		s.logger.Info("maintain: 重複グループは見つかりませんでした")
		return &MaintainResult{}, nil
	}
	s.logger.Info("maintain: 重複グループを検出", "groups", len(groups))

	// フェーズ4: バッチLLM呼び出しで各グループを判定する。
	var totalDeleted, totalMerged int
	batchSize := opts.MaxGroupsPerLLMCall
	if batchSize <= 0 {
		batchSize = 5
	}

	for batchStart := 0; batchStart < len(groups); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(groups) {
			batchEnd = len(groups)
		}
		batch := groups[batchStart:batchEnd]

		decisions, err := s.judgeBatch(ctx, batch)
		if err != nil {
			s.logger.Error("maintain: LLM判定バッチでエラー", "error", err)
			continue
		}

		for _, d := range decisions {
			if opts.DryRun {
				s.logger.Info("maintain: [dry-run]",
					"action", d.action,
					"keep", d.keepID,
					"delete", d.deleteIDs,
					"reason", d.reason)
				continue
			}

			switch d.action {
			case "keep":
				n, err := s.admin.DeleteBatch(ctx, d.deleteIDs)
				if err != nil {
					s.logger.Error("maintain: 削除に失敗", "error", err, "ids", d.deleteIDs)
				}
				totalDeleted += n
			case "merge":
				if err := s.executeMerge(ctx, d); err != nil {
					s.logger.Error("maintain: 統合に失敗", "error", err)
				} else {
					totalMerged++
					totalDeleted += len(d.deleteIDs)
				}
			}
		}
	}

	s.logger.Info("maintain: 完了",
		"groups", len(groups),
		"deleted", totalDeleted,
		"merged", totalMerged)

	return &MaintainResult{
		Groups:       len(groups),
		TotalDeleted: totalDeleted,
		TotalMerged:  totalMerged,
	}, nil
}

// executeMerge はグループの全メンバーを削除し、新しい統合メモリを保存する。
func (s *Server) executeMerge(ctx context.Context, d decision) error {
	if _, err := s.admin.DeleteBatch(ctx, d.deleteIDs); err != nil {
		s.logger.Warn("maintain: 統合時の削除に失敗", "error", err, "ids", d.deleteIDs)
	}
	mem := buildMergedMemory(d)
	return s.store.Save(ctx, mem)
}

// memoriesToEntries は []memory.Memory をメンテナンス用の軽量 []memEntry に変換する。
func memoriesToEntries(mems []memory.Memory) []memEntry {
	entries := make([]memEntry, len(mems))
	for i, m := range mems {
		entries[i] = memEntry{
			id:        m.ID,
			memType:   m.Type,
			content:   m.Content,
			metadata:  m.Metadata,
			persons:   m.Persons,
			keywords:  m.Keywords,
			topic:     m.Topic,
			createdAt: m.CreatedAt,
		}
	}
	return entries
}
