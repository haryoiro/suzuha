package langfuse

import (
	"context"

	"github.com/haryoiro/suzuha/internal/event"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// PerceiveInfo はパイプライン知覚ステージの結果 (consumer-side 型)。
type PerceiveInfo struct {
	Channel           string
	IsDM              bool
	DirectlyAddressed bool
	SenderIsBot       bool
	UserID            string
	UserName          string
	Content           string
	Source            string
}

// ThinkInfo はパイプライン思考ステージの結果 (consumer-side 型)。
type ThinkInfo struct {
	ListenMode      bool
	BackgroundCount int
	ForegroundCount int
	Directive       string
}

// Hook は OpenTelemetry スパンを生成するパイプラインフック。
type Hook struct {
	tracer trace.Tracer
}

// NewHook は指定された tracer で Langfuse パイプラインフックを作成する。
func NewHook(tracer trace.Tracer) *Hook {
	return &Hook{tracer: tracer}
}

// BeforePipeline はターン全体のルートスパンを開始する。
func (h *Hook) BeforePipeline(ctx context.Context) context.Context {
	ctx, _ = h.tracer.Start(ctx, "pipeline.turn")
	return ctx
}

// EndPipeline はルートスパンを閉じる。
func (h *Hook) EndPipeline(ctx context.Context) {
	span := trace.SpanFromContext(ctx)
	span.End()
}

// AfterPerceive は知覚ステージ完了後にスパンを記録する。
func (h *Hook) AfterPerceive(ctx context.Context, batch []event.Event, p *PerceiveInfo) error {
	rootSpan := trace.SpanFromContext(ctx)
	rootSpan.SetAttributes(
		attribute.String("suzuha.channel", p.Channel),
		attribute.String("suzuha.user_id", p.UserID),
		attribute.String("suzuha.user_name", p.UserName),
		attribute.String("suzuha.source", p.Source),
		attribute.String("suzuha.input", p.Content),
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

// AfterThink は思考ステージ完了後にスパンを記録する。
func (h *Hook) AfterThink(ctx context.Context, t *ThinkInfo) error {
	_, span := h.tracer.Start(ctx, "pipeline.think",
		trace.WithAttributes(
			attribute.Bool("suzuha.listen_mode", t.ListenMode),
			attribute.Int("suzuha.background_count", t.BackgroundCount),
			attribute.Int("suzuha.foreground_count", t.ForegroundCount),
			attribute.String("suzuha.directive", textutil.TruncateRunes(t.Directive, 100)),
		),
	)
	span.End()
	return nil
}

// AfterAct はアクトステージ完了後にスパンを記録する。
func (h *Hook) AfterAct(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "pipeline.act")
	span.End()
	return nil
}

// AfterReflect はリフレクトステージ完了後にスパンを記録する。
func (h *Hook) AfterReflect(ctx context.Context) error {
	_, span := h.tracer.Start(ctx, "pipeline.reflect")
	span.End()
	return nil
}
