package scheduler

import (
	"context"
	"encoding/json"
)

// CronTask は scheduler が実行する定期ジョブの契約。
// 各 task は constructor で必要な依存を受け取り、Execute 時点では ctx と
// job 固有 cfg のみを受け取る (神オブジェクトを経由しない)。
type CronTask interface {
	// Name は task の一意識別子を返す (e.g. "topics", "explore")。
	// config.yaml の job 定義の "task" フィールドと突き合わせされる。
	Name() string

	// Description は人間向けの説明を返す。
	Description() string

	// Setup は scheduler 起動時に 1 回呼ばれる。マイグレーションや初期化に使う。
	Setup(ctx context.Context) error

	// Execute は task を 1 回実行する。cfg は config.yaml の job 定義から渡される。
	Execute(ctx context.Context, cfg json.RawMessage) error
}
