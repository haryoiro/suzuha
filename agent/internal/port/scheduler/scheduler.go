// Package scheduler は cron スケジューラに登録できる Task の契約 interface を定義する。
// 実装は各 behavior / capability の `task.go` に配置する。
//
// 現行の `internal/scheduler.CronTask` (CronContext 経由で LLM/Memory/Notifier 等に
// アクセス) はそのまま残し、runtime/scheduler/ 内部型として Phase 9 まで共存する。
// 本 port.Task は Feature interface 廃止後の「最小契約」を先取りして定義しており、
// 新規 Task 実装はこちらを目指す。
package scheduler

import (
	"context"
	"encoding/json"
)

// Task はスケジューラが動かせる最小の Task 契約。
// Setup は scheduler 起動時に 1 回だけ呼ばれる (nil context もあり得る)。
// Execute は 1 回分のジョブ実行で、cfg は config.yaml の job エントリが JSON 化されたもの。
type Task interface {
	Name() string
	Description() string
	Execute(ctx context.Context, cfg json.RawMessage) error
}
