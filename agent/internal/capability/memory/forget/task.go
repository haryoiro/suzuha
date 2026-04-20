package forget

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
)

// consolidator は forget が必要とする統合機能を定義する (consumer-side interface)。
// capability sibling (consolidate) を直接 import せず、共有型は domain/memo 経由。
type consolidator interface {
	Consolidate(ctx context.Context, opts *memo.ConsolidateOpts) (*memo.ConsolidateResult, error)
}

// Task は scheduler.CronTask を実装する薄いアダプタで、
// 実際の重複排除・統合ロジックは consolidator に委譲する。
type Task struct {
	consolidator consolidator
	db           *sql.DB
	logger       *slog.Logger
}

// NewTask は forget Task を生成する。db は状態永続化用で nil 可。
func NewTask(c consolidator, db *sql.DB, logger *slog.Logger) *Task {
	return &Task{consolidator: c, db: db, logger: logger}
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "forget" }
func (t *Task) Description() string { return "類似記憶の重複排除・統合" }

// Setup は forget には初期化不要。
func (t *Task) Setup(_ context.Context) error {
	return nil
}

// Execute は Consolidator に重複排除を依頼し、統計を永続化する。
func (t *Task) Execute(ctx context.Context, cfg json.RawMessage) error {
	opts := memo.ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &opts); err != nil {
			t.logger.Warn("forget: 設定の解析に失敗", "error", err)
		}
	}

	result, err := t.consolidator.Consolidate(ctx, &opts)
	if err != nil {
		return err
	}

	if t.db != nil {
		scheduler.SaveState(ctx, t.db, t.Name(), &persistedState{
			LastRunAt:    time.Now(),
			TotalDeleted: result.TotalDeleted,
			TotalMerged:  result.TotalMerged,
		})
	}
	return nil
}

type persistedState struct {
	LastRunAt    time.Time `json:"last_run_at"`
	TotalDeleted int       `json:"total_deleted"`
	TotalMerged  int       `json:"total_merged"`
}
