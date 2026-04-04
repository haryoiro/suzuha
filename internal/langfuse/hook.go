package langfuse

import (
	"context"
	"fmt"

	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/event"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Hook implements agent.PipelineHook and agent.PipelineSpanner,
// creating OpenTelemetry spans for each pipeline stage.
type Hook struct {
	tracer trace.Tracer
}

var (
	_ agent.PipelineHook    = (*Hook)(nil)
	_ agent.PipelineSpanner = (*Hook)(nil)
)

// NewHook creates a Langfuse pipeline hook with the given tracer.
func NewHook(tracer trace.Tracer) *Hook {
	return &Hook{tracer: tracer}
}

// BeforePipeline starts a root span for the entire turn.
func (h *Hook) BeforePipeline(ctx context.Context) context.Context {
	ctx, _ = h.tracer.Start(ctx, "pipeline.turn")
	return ctx
}

// EndPipeline closes the root span.
func (h *Hook) EndPipeline(ctx context.Context) {
	span := trace.SpanFromContext(ctx)
	span.End()
}

func (h *Hook) AfterPerceive(ctx context.Context, batch []event.Event, p *agent.Perception) error {
	// Enrich the root turn span with user/channel info.
	rootSpan := trace.SpanFromContext(ctx)
	rootSpan.SetAttributes(
		attribute.String("suzuha.channel", p.Channel),
		attribute.String("suzuha.user_id", p.LastMessage.UserID),
		attribute.String("suzuha.user_name", p.LastMessage.UserName),
		attribute.String("suzuha.source", string(p.LastEvent.Source)),
		attribute.String("suzuha.input", p.LastMessage.Content),
	)

	_, span := h.tracer.Start(ctx, "pipeline.perceive",
		trace.WithAttributes(
			attribute.String("suzuha.channel", p.Channel),
			attribute.Bool("suzuha.is_dm", p.IsDM),
			attribute.Bool("suzuha.directly_addressed", p.DirectlyAddressed),
			attribute.Bool("suzuha.sender_is_bot", p.SenderIsBot),
			attribute.Int("suzuha.batch_size", len(batch)),
		),
	)
	span.End()
	return nil
}

func (h *Hook) AfterThink(ctx context.Context, p *agent.Perception, t *agent.Thought) error {
	_, span := h.tracer.Start(ctx, "pipeline.think",
		trace.WithAttributes(
			attribute.Bool("suzuha.listen_mode", t.ListenMode),
			attribute.Int("suzuha.background_count", len(t.Background)),
			attribute.Int("suzuha.foreground_count", len(t.Foreground)),
			attribute.String("suzuha.directive", truncate(t.Directive, 100)),
		),
	)
	span.End()
	return nil
}

func (h *Hook) AfterAct(ctx context.Context, p *agent.Perception, t *agent.Thought) error {
	_, span := h.tracer.Start(ctx, "pipeline.act")
	span.End()
	return nil
}

func (h *Hook) AfterReflect(ctx context.Context, p *agent.Perception) error {
	_, span := h.tracer.Start(ctx, "pipeline.reflect")
	span.End()
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return fmt.Sprintf("%s...", string(r[:n]))
}
