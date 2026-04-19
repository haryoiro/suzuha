package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// JobDef defines a scheduled job from configuration.
type JobDef struct {
	Name   string         // human-readable job name
	Task   string         // CronTask.Name() to match
	Cron   string         // cron expression (e.g. "*/30 * * * *", "@every 10s")
	Config map[string]any // task-specific config, marshaled to JSON for Execute()
}

// JobStatus represents the runtime status of a scheduled job.
type JobStatus struct {
	Name   string         `json:"name"`
	Task   string         `json:"task"`
	Cron   string         `json:"cron"`
	Config map[string]any `json:"config,omitempty"`
	Prev   time.Time      `json:"prev"`
	Next   time.Time      `json:"next"`
}

// jobMeta holds the mapping between a cron entry ID and job metadata.
type jobMeta struct {
	entryID cron.EntryID
	name    string
	task    string
	cronExpr string
	config  map[string]any
}

// Scheduler manages periodic CronTask execution in the Consolidator process.
type Scheduler struct {
	cron     *cron.Cron
	registry *TaskRegistry
	cc       *CronContext
	logger   *slog.Logger

	mu      sync.Mutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    []jobMeta
}

// New creates a Scheduler.
func New(registry *TaskRegistry, cc *CronContext, logger *slog.Logger) *Scheduler {
	loc := cc.Timezone
	if loc == nil {
		loc = time.UTC
	}
	c := cron.New(
		cron.WithLocation(loc),
		cron.WithLogger(cron.VerbosePrintfLogger(slog.NewLogLogger(logger.Handler(), slog.LevelDebug))),
		cron.WithChain(cron.Recover(cron.VerbosePrintfLogger(slog.NewLogLogger(logger.Handler(), slog.LevelError)))),
	)
	return &Scheduler{
		cron:     c,
		registry: registry,
		cc:       cc,
		logger:   logger,
	}
}

// Setup calls Setup() on all registered tasks.
func (s *Scheduler) Setup(ctx context.Context) error {
	for _, t := range s.registry.All() {
		s.logger.Info("scheduler: タスクをセットアップ中", "task", t.Name())
		if err := t.Setup(ctx, s.cc); err != nil {
			return fmt.Errorf("scheduler: %s のセットアップに失敗: %w", t.Name(), err)
		}
	}
	return nil
}

// LoadJobs registers job definitions from configuration.
func (s *Scheduler) LoadJobs(jobs []JobDef) error {
	for _, j := range jobs {
		task, ok := s.registry.Get(j.Task)
		if !ok {
			s.logger.Warn("scheduler: 不明なタスク、ジョブをスキップします", "job", j.Name, "task", j.Task)
			continue
		}

		cfg, err := json.Marshal(j.Config)
		if err != nil {
			return fmt.Errorf("scheduler: %s の設定のマーシャルに失敗: %w", j.Name, err)
		}

		jobName := j.Name
		taskName := j.Task
		entryID, err := s.cron.AddFunc(j.Cron, func() {
			s.logger.Debug("scheduler: ジョブを実行中", "job", jobName, "task", taskName)
			jobCtx := s.ctx
			if jobCtx == nil {
				jobCtx = context.Background()
			}
			if execErr := task.Execute(jobCtx, s.cc, cfg); execErr != nil {
				s.logger.Error("scheduler: ジョブが失敗しました", "job", jobName, "task", taskName, "error", execErr)
			}
		})
		if err != nil {
			return fmt.Errorf("scheduler: ジョブ %s の追加に失敗 (cron=%q): %w", j.Name, j.Cron, err)
		}
		s.jobs = append(s.jobs, jobMeta{
			entryID:  entryID,
			name:     j.Name,
			task:     j.Task,
			cronExpr: j.Cron,
			config:   j.Config,
		})
		s.logger.Info("scheduler: ジョブを登録しました", "job", j.Name, "task", task.Name(), "cron", j.Cron)
	}
	return nil
}

// Start begins executing registered jobs on their schedules.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.cron.Start()
	s.logger.Info("scheduler: 開始しました", "entries", len(s.cron.Entries()))
}

// Stop gracefully stops the scheduler, waiting for running jobs to complete.
func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if s.cancel != nil {
		s.cancel()
	}
	ctx := s.cron.Stop()
	s.logger.Info("scheduler: 停止しました")
	return ctx
}

// Entries returns the number of registered cron entries (for testing/metrics).
func (s *Scheduler) Entries() int {
	return len(s.cron.Entries())
}

// ListJobs returns the status of all registered jobs including next/prev run times.
func (s *Scheduler) ListJobs() []JobStatus {
	entries := s.cron.Entries()
	entryMap := make(map[cron.EntryID]cron.Entry, len(entries))
	for _, e := range entries {
		entryMap[e.ID] = e
	}

	result := make([]JobStatus, 0, len(s.jobs))
	for _, jm := range s.jobs {
		js := JobStatus{
			Name:   jm.name,
			Task:   jm.task,
			Cron:   jm.cronExpr,
			Config: jm.config,
		}
		if e, ok := entryMap[jm.entryID]; ok {
			js.Prev = e.Prev
			js.Next = e.Next
		}
		result = append(result, js)
	}
	return result
}

// TriggerTask executes a registered task immediately by name.
// cfg is optional task-specific config (JSON); if nil, falls back to the
// configured job's default config for any job whose task matches taskName.
func (s *Scheduler) TriggerTask(ctx context.Context, taskName string, cfg json.RawMessage) error {
	task, ok := s.registry.Get(taskName)
	if !ok {
		return fmt.Errorf("不明なタスク: %s", taskName)
	}
	if cfg == nil {
		// 設定済みジョブの default config を拾う。
		s.mu.Lock()
		for _, m := range s.jobs {
			if m.task == taskName && m.config != nil {
				if b, err := json.Marshal(m.config); err == nil {
					cfg = b
				}
				break
			}
		}
		s.mu.Unlock()
	}
	s.logger.Info("scheduler: 手動トリガー", "task", taskName)
	return task.Execute(ctx, s.cc, cfg)
}
