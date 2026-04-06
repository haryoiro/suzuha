package scheduler

import "sync"

// TaskRegistry is a thread-safe collection of available CronTask implementations.
// It mirrors tool.Registry in design.
type TaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]CronTask
}

// NewRegistry creates an empty task registry.
func NewRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]CronTask)}
}

// Register adds a CronTask to the registry.
func (r *TaskRegistry) Register(t CronTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[t.Name()] = t
}

// Get returns a CronTask by name.
func (r *TaskRegistry) Get(name string) (CronTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[name]
	return t, ok
}

// All returns all registered tasks.
func (r *TaskRegistry) All() []CronTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CronTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t)
	}
	return out
}
