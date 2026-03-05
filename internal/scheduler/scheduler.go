package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
)

// JobDef defines a scheduled job from configuration.
type JobDef struct {
	Name   string         // human-readable job name
	Task   string         // CronTask.Name() to match
	Cron   string         // cron expression (e.g. "*/30 * * * *", "@every 10s")
	Config map[string]any // task-specific config, marshaled to JSON for Execute()
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
}

// New creates a Scheduler.
func New(registry *TaskRegistry, cc *CronContext, logger *slog.Logger) *Scheduler {
	c := cron.New(
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
		_, err = s.cron.AddFunc(j.Cron, func() {
			s.logger.Info("scheduler: ジョブを実行中", "job", jobName, "task", taskName)
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

// TriggerTask executes a registered task immediately by name.
// cfg is optional task-specific config (JSON); if nil, the task's default config is used.
func (s *Scheduler) TriggerTask(ctx context.Context, taskName string, cfg json.RawMessage) error {
	task, ok := s.registry.Get(taskName)
	if !ok {
		return fmt.Errorf("不明なタスク: %s", taskName)
	}
	s.logger.Info("scheduler: 手動トリガー", "task", taskName)
	return task.Execute(ctx, s.cc, cfg)
}
