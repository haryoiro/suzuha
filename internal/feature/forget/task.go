package forget

import (
	"context"
	"encoding/json"
	"time"

	"github.com/haryoiro/suzuha/internal/memento"
	"github.com/haryoiro/suzuha/internal/scheduler"
)

// Task は scheduler.CronTask を実装する薄いアダプタで、
// 実際の重複排除・統合ロジックは consolidator インターフェースに委譲する。
type Task struct {
	consolidator consolidator
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "forget" }
func (t *Task) Description() string { return "類似記憶の重複排除・統合" }

func (t *Task) Setup(_ context.Context, _ *scheduler.CronContext) error {
	return nil
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	// ConsolidateOpts を直接 unmarshal する（json タグ付き）。
	opts := memento.ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
	}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &opts); err != nil {
			cc.Logger.Warn("forget: 設定の解析に失敗", "error", err)
		}
	}

	result, err := t.consolidator.Consolidate(ctx, &opts)
	if err != nil {
		return err
	}

	// 管理ダッシュボード用に状態を永続化する（UI表示用の last_run_at を含む）。
	if cc.DB != nil {
		scheduler.SaveState(ctx, cc.DB, t.Name(), &persistedState{
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
