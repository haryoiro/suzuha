package control

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/samber/do/v2"
)

// SchedulerHandler は Scheduler グループ (jobs / trigger) を実装する。
type SchedulerHandler struct {
	scheduler *scheduler.Scheduler // nil のときは空レスポンスを返す
}

// NewSchedulerHandler は DI injector から依存を取り出して SchedulerHandler を生成する。
func NewSchedulerHandler(i do.Injector) (gen.SchedulerHandler, error) {
	return &SchedulerHandler{
		scheduler: do.MustInvoke[*scheduler.Scheduler](i),
	}, nil
}

// SchedulerJobs implements GET /internal/scheduler/jobs.
func (h *SchedulerHandler) SchedulerJobs(ctx context.Context) (*gen.SchedulerJobsResponse, error) {
	if h.scheduler == nil {
		return &gen.SchedulerJobsResponse{Data: []gen.SchedulerJob{}}, nil
	}
	jobs := h.scheduler.ListJobs()
	data := make([]gen.SchedulerJob, len(jobs))
	for i, j := range jobs {
		data[i] = gen.SchedulerJob{
			Name: j.Name,
			Task: j.Task,
			Cron: j.Cron,
			Prev: j.Prev.Format(time.RFC3339),
			Next: j.Next.Format(time.RFC3339),
		}
		if j.Config != nil {
			data[i].Config = gen.NewOptSchedulerJobConfig(toJxMap(j.Config))
		}
	}
	return &gen.SchedulerJobsResponse{Data: data}, nil
}

// SchedulerTrigger implements POST /internal/trigger/{task}.
func (h *SchedulerHandler) SchedulerTrigger(ctx context.Context, req *gen.TriggerRequest, params gen.SchedulerTriggerParams) (*gen.TriggerResponse, error) {
	if h.scheduler == nil {
		return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString("scheduler not enabled")}, nil
	}
	var cfg json.RawMessage
	if req != nil && req.Config.Set {
		b, err := json.Marshal(req.Config.Value)
		if err != nil {
			return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString("config marshal failed")}, nil
		}
		cfg = b
	}
	if err := h.scheduler.TriggerTask(ctx, params.Task, cfg); err != nil {
		return &gen.TriggerResponse{Ok: false, Error: gen.NewOptString(err.Error())}, nil
	}
	return &gen.TriggerResponse{Ok: true}, nil
}

// toJxMap は map[string]any を ogen の map[string]jx.Raw に変換する。
// Marshal 失敗時はその key をスキップ。
func toJxMap(m map[string]any) gen.SchedulerJobConfig {
	out := make(gen.SchedulerJobConfig, len(m))
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		out[k] = jx.Raw(b)
	}
	return out
}
