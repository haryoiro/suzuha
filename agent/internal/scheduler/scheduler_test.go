package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// echoTask is a minimal CronTask for testing.
type echoTask struct {
	setupCalled atomic.Bool
	execCount   atomic.Int32
}

func (t *echoTask) Name() string        { return "echo" }
func (t *echoTask) Description() string { return "test echo task" }

func (t *echoTask) Setup(ctx context.Context, cc *CronContext) error {
	t.setupCalled.Store(true)
	return nil
}

func (t *echoTask) Execute(ctx context.Context, cc *CronContext, cfg json.RawMessage) error {
	t.execCount.Add(1)
	cc.Logger.Info("echo task executed", "config", string(cfg))
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
	task := &echoTask{}
	reg := NewRegistry()
	reg.Register(task)

	cc := &CronContext{Logger: logger}
	sched := New(reg, cc, logger)

	if err := sched.Setup(context.Background()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !task.setupCalled.Load() {
		t.Error("expected Setup() to be called on echo task")
	}
}

func TestSchedulerLoadAndRun(t *testing.T) {
	logger := slog.Default()
	task := &echoTask{}
	reg := NewRegistry()
	reg.Register(task)

	cc := &CronContext{Logger: logger}
	sched := New(reg, cc, logger)

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
	defer sched.Stop()

	// Wait for at least one execution.
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
	cc := &CronContext{Logger: logger}
	sched := New(reg, cc, logger)

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
