package agent

import (
	"context"

	"github.com/haryoiro/suzuha/internal/event"
)

// PipelineHook allows external components to observe and react to pipeline stages.
// Features that implement this interface (in addition to scheduler.Feature)
// can be registered via Agent.AddHook and will be called at each stage.
type PipelineHook interface {
	AfterPerceive(ctx context.Context, batch []event.Event, p *Perception) error
	AfterThink(ctx context.Context, p *Perception, t *Thought) error
	AfterAct(ctx context.Context, p *Perception, t *Thought) error
	AfterReflect(ctx context.Context, p *Perception) error
}

// AddHook registers a pipeline hook. Hooks are called in registration order.
func (a *Agent) AddHook(h PipelineHook) {
	a.hooks = append(a.hooks, h)
}

// runHooks calls fn on each registered hook. Errors are logged but do not
// interrupt processing — hooks are advisory, not blocking.
func (a *Agent) runHooks(fn func(PipelineHook) error) {
	for _, h := range a.hooks {
		if err := fn(h); err != nil {
			a.logger.Warn("フックでエラーが起きた", "error", err)
		}
	}
}
