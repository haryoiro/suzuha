package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// echoTask は最小限の CronTask テスト実装。
type echoTask struct {
	setupCalled atomic.Bool
	execCount   atomic.Int32
	logger      *slog.Logger
}

func (t *echoTask) Name() string        { return "echo" }
func (t *echoTask) Description() string { return "test echo task" }

func (t *echoTask) Setup(_ context.Context) error {
	t.setupCalled.Store(true)
	return nil
}

func (t *echoTask) Execute(_ context.Context, cfg json.RawMessage) error {
	t.execCount.Add(1)
	if t.logger != nil {
		t.logger.Info("echo task executed", "config", string(cfg))
	}
	return nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	task := &echoTask{}
	reg.Register(task)

	got, ok := reg.Get("echo")
	if !ok {
		t.Fatal("expected to find registered task 'echo'")
	}
	if got.Name() != "echo" {
		t.Errorf("got name %q, want %q", got.Name(), "echo")
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Fatal("expected nonexistent task to not be found")
	}
}

func TestRegistryAll(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&echoTask{})

	all := reg.All()
	if len(all) != 1 {
		t.Fatalf("got %d tasks, want 1", len(all))
	}
}

func TestSchedulerSetup(t *testing.T) {
	logger := slog.Default()
	task := &echoTask{logger: logger}
	reg := NewRegistry()
	reg.Register(task)

	sched := New(reg, nil, logger)

	if err := sched.Setup(context.Background()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !task.setupCalled.Load() {
		t.Error("expected Setup() to be called on echo task")
	}
}

func TestSchedulerLoadAndRun(t *testing.T) {
	logger := slog.Default()
	task := &echoTask{logger: logger}
	reg := NewRegistry()
	reg.Register(task)

	sched := New(reg, nil, logger)

	jobs := []JobDef{
		{
			Name:   "test-echo",
			Task:   "echo",
			Cron:   "@every 1s",
			Config: map[string]any{"message": "hello"},
		},
	}

	if err := sched.LoadJobs(jobs); err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if sched.Entries() != 1 {
		t.Fatalf("got %d entries, want 1", sched.Entries())
	}

	sched.Start()
	t.Cleanup(func() { sched.Stop() })

	deadline := time.After(3 * time.Second)
	for task.execCount.Load() <= 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for task execution")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func TestSchedulerUnknownTaskSkipped(t *testing.T) {
	logger := slog.Default()
	reg := NewRegistry()
	sched := New(reg, nil, logger)

	jobs := []JobDef{
		{
			Name: "missing-task",
			Task: "nonexistent",
			Cron: "@every 1s",
		},
	}

	if err := sched.LoadJobs(jobs); err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if sched.Entries() != 0 {
		t.Fatalf("got %d entries, want 0 (unknown task should be skipped)", sched.Entries())
	}
}
