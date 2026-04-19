package di

import (
	"context"

	"github.com/haryoiro/suzuha/internal/observe/langfuse"
	"github.com/haryoiro/suzuha/internal/runtime/agent"
	"github.com/haryoiro/suzuha/internal/runtime/event"
)

// langfuseAdapter は langfuse.Hook を agent.PipelineHook に適合させるアダプタ。
// cmd 層は全レイヤーを import できるため、ここで agent 型 → langfuse 型の変換を行う。
type langfuseAdapter struct {
	hook *langfuse.Hook
}

var (
	_ agent.PipelineHook    = (*langfuseAdapter)(nil)
	_ agent.PipelineSpanner = (*langfuseAdapter)(nil)
)

func newLangfuseAdapter(hook *langfuse.Hook) *langfuseAdapter {
	return &langfuseAdapter{hook: hook}
}

func (a *langfuseAdapter) BeforePipeline(ctx context.Context) context.Context {
	return a.hook.BeforePipeline(ctx)
}

func (a *langfuseAdapter) EndPipeline(ctx context.Context) {
	a.hook.EndPipeline(ctx)
}

func (a *langfuseAdapter) AfterPerceive(ctx context.Context, batch []event.Event, p *agent.Perception) error {
	info := &langfuse.PerceiveInfo{
		Channel:           p.Channel,
		IsDM:              p.IsDM,
		DirectlyAddressed: p.DirectlyAddressed,
		SenderIsBot:       p.SenderIsBot,
		UserID:            p.LastMessage.UserID,
		UserName:          p.LastMessage.UserName,
		Content:           p.LastMessage.Content,
		Source:            p.LastEvent.Source,
	}
	return a.hook.AfterPerceive(ctx, batch, info)
}

func (a *langfuseAdapter) AfterThink(ctx context.Context, _ *agent.Perception, t *agent.Thought) error {
	info := &langfuse.ThinkInfo{
		ListenMode:      t.ListenMode,
		BackgroundCount: len(t.Background),
		ForegroundCount: len(t.Foreground),
		Directive:       t.Directive,
	}
	return a.hook.AfterThink(ctx, info)
}

func (a *langfuseAdapter) AfterAct(ctx context.Context, _ *agent.Perception, _ *agent.Thought) error {
	return a.hook.AfterAct(ctx)
}

func (a *langfuseAdapter) AfterReflect(ctx context.Context, _ *agent.Perception) error {
	return a.hook.AfterReflect(ctx)
}
