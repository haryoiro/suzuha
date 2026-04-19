package agent

import (
	"context"

	"github.com/haryoiro/suzuha/internal/runtime/event"
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

// PipelineSpanner is an optional extension that hooks can implement to manage
// turn-level spans. BeforePipeline returns a context with a root span, and
// EndPipeline closes it.
type PipelineSpanner interface {
	BeforePipeline(ctx context.Context) context.Context
	EndPipeline(ctx context.Context)
}

// AddHook registers a pipeline hook. Hooks are called in registration order.
func (a *Agent) AddHook(h PipelineHook) {
	a.hooks = append(a.hooks, h)
}

// runHooksWithCtx calls fn on each registered hook, passing the given context.
// Errors are logged but do not interrupt processing.
func (a *Agent) runHooksWithCtx(ctx context.Context, fn func(context.Context, PipelineHook) error) {
	for _, h := range a.hooks {
		if err := fn(ctx, h); err != nil {
			a.logger.Warn("フックでエラーが起きた", "error", err)
		}
	}
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

// beginPipelineHooks calls BeforePipeline on hooks that implement PipelineSpanner,
// returning a context enriched with tracing spans.
func (a *Agent) beginPipelineHooks(ctx context.Context) context.Context {
	for _, h := range a.hooks {
		if s, ok := h.(PipelineSpanner); ok {
			ctx = s.BeforePipeline(ctx)
		}
	}
	return ctx
}

// endPipelineHooks calls EndPipeline on hooks that implement PipelineSpanner.
func (a *Agent) endPipelineHooks(ctx context.Context) {
	for _, h := range a.hooks {
		if s, ok := h.(PipelineSpanner); ok {
			s.EndPipeline(ctx)
		}
	}
}
