package consolidator

import (
	"context"
	"fmt"
	"log/slog"
	"time"


	"github.com/haryoiro/suzuha/internal/memory"
)

// memoryAdmin は Consolidator が使用するメモリ管理操作の consumer-side interface。
type memoryAdmin interface {
	ListEmbeddedMemories(ctx context.Context) ([]memory.Memory, error)
	ListAllEmbeddings(ctx context.Context) (map[string][]float32, error)
	DeleteBatch(ctx context.Context, ids []string) (int, error)
}

// memorySaver は統合結果の保存を行う consumer-side interface。
type memorySaver interface {
	Save(ctx context.Context, mem *memory.Memory) error
}

// Consolidator は既存メモリの重複排除・マージを実行する。
type Consolidator struct {
	llm    Completer
	admin  memoryAdmin
	store  memorySaver
	logger *slog.Logger
}

// NewConsolidator は Consolidator を作成する。
func NewConsolidator(llm Completer, admin memoryAdmin, store memorySaver, logger *slog.Logger) *Consolidator {
	return &Consolidator{llm: llm, admin: admin, store: store, logger: logger}
}

// Consolidate はメモリの重複排除と統合パイプラインを実行する。
func (c *Consolidator) Consolidate(ctx context.Context, opts *ConsolidateOpts) (*ConsolidateResult, error) {
	if c.admin == nil {
		return nil, fmt.Errorf("consolidate: AdminStore が未設定、実行不可")
	}

	c.logger.Info("consolidate: 開始",
		"similarity_threshold", opts.SimilarityThreshold,
		"dry_run", opts.DryRun)

	memories, err := c.admin.ListEmbeddedMemories(ctx)
	if err != nil {
		return nil, fmt.Errorf("consolidate: エントリの読み込みに失敗: %w", err)
	}
	entries := memoriesToEntries(memories)
	if len(entries) < 2 {
		c.logger.Info("consolidate: 重複排除に必要な記憶数が不足", "count", len(entries))
		return &ConsolidateResult{}, nil
	}
	c.logger.Info("consolidate: 記憶を読み込み完了", "count", len(entries))

	embeddings, err := c.admin.ListAllEmbeddings(ctx)
	if err != nil {
		return nil, fmt.Errorf("consolidate: 埋め込みの読み込みに失敗: %w", err)
	}

	groups := buildSimilarityGroups(entries, embeddings, opts.SimilarityThreshold, opts.MaxGroupSize)
	if len(groups) == 0 {
		c.logger.Info("consolidate: 重複グループは見つかりませんでした")
		return &ConsolidateResult{}, nil
	}
	c.logger.Info("consolidate: 重複グループを検出", "groups", len(groups))

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

		decisions, err := c.judgeBatch(ctx, batch)
		if err != nil {
			c.logger.Error("consolidate: LLM判定バッチでエラー", "error", err)
			continue
		}

		for _, d := range decisions {
			if opts.DryRun {
				c.logger.Info("consolidate: [dry-run]",
					"action", d.action,
					"keep", d.keepID,
					"delete", d.deleteIDs,
					"reason", d.reason)
				continue
			}

			switch d.action {
			case "keep":
				n, err := c.admin.DeleteBatch(ctx, d.deleteIDs)
				if err != nil {
					c.logger.Error("consolidate: 削除に失敗", "error", err, "ids", d.deleteIDs)
				}
				totalDeleted += n
			case "merge":
				if err := c.executeMerge(ctx, d); err != nil {
					c.logger.Error("consolidate: 統合に失敗", "error", err)
				} else {
					totalMerged++
					totalDeleted += len(d.deleteIDs)
				}
			}
		}
	}

	c.logger.Info("consolidate: 完了",
		"groups", len(groups),
		"deleted", totalDeleted,
		"merged", totalMerged)

	return &ConsolidateResult{
		Groups:       len(groups),
		TotalDeleted: totalDeleted,
		TotalMerged:  totalMerged,
	}, nil
}

func (c *Consolidator) executeMerge(ctx context.Context, d decision) error {
	if _, err := c.admin.DeleteBatch(ctx, d.deleteIDs); err != nil {
		c.logger.Warn("consolidate: 統合時の削除に失敗", "error", err, "ids", d.deleteIDs)
	}
	mem := buildMergedMemory(d)
	return c.store.Save(ctx, mem)
}

// --- 内部型 ---

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

type memoryGroup struct {
	memType memory.MemoryType
	members []memEntry
}

type decision struct {
	action        string
	keepID        string
	deleteIDs     []string
	mergedContent string
	groupType     memory.MemoryType
	reason        string
	sourceEntries []memEntry
}

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
