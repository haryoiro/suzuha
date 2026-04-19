package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
	actionDom "github.com/haryoiro/suzuha/internal/domain/action"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/robfig/cron/v3"
)

func actionToAPI(a actionDom.Action) gen.ScheduledAction {
	sa := gen.ScheduledAction{
		ID:          a.ID,
		ChannelID:   a.ChannelID,
		Content:     a.Content,
		Mode:        gen.ScheduledActionMode(a.Mode),
		ScheduledAt: a.ScheduledAt.Format(time.RFC3339),
		Status:      gen.ScheduledActionStatus(a.Status),
		RetryCount:  int32(a.RetryCount),
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
	}
	if a.CronExpr != "" {
		sa.CronExpr = gen.NewOptString(a.CronExpr)
	}
	if a.CreatedBy != "" {
		sa.CreatedBy = gen.NewOptString(a.CreatedBy)
	}
	if a.ExecutedAt != nil {
		sa.ExecutedAt = gen.NewOptString(a.ExecutedAt.Format(time.RFC3339))
	}
	return sa
}

func (h *AdminHandler) ScheduledActionsList(ctx context.Context, params gen.ScheduledActionsListParams) (*gen.ScheduledActionsListOK, error) {
	limit := int(params.Limit.Or(50))

	actions, err := h.schedStore.List(ctx, actionDom.ListOpts{
		Status: params.Status.Or(""),
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.ScheduledAction, 0, len(actions))
	for _, a := range actions {
		data = append(data, actionToAPI(a))
	}
	return &gen.ScheduledActionsListOK{Data: data}, nil
}

func (h *AdminHandler) ScheduledActionsCreate(ctx context.Context, req *gen.CreateActionRequest) (*gen.ScheduledActionsCreateCreated, error) {
	var scheduledAt time.Time

	if v, ok := req.ScheduledAt.Get(); ok && v != "" {
		parsed, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return nil, fmt.Errorf("scheduled_at must be RFC3339 format")
		}
		scheduledAt = parsed.UTC()
	} else if v, ok := req.CronExpr.Get(); ok && v != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, parseErr := parser.Parse(v)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid cron_expr: %s", parseErr.Error())
		}
		scheduledAt = sched.Next(jtime.Now()).UTC()
	} else {
		return nil, fmt.Errorf("either scheduled_at or cron_expr is required")
	}

	mode := string(req.Mode.Or(gen.CreateActionRequestModePrompt))
	cronExpr := req.CronExpr.Or("")

	a := &actionDom.Action{
		ChannelID:   req.ChannelID,
		Content:     req.Content,
		Mode:        mode,
		ScheduledAt: scheduledAt,
		CronExpr:    cronExpr,
		CreatedBy:   "admin",
	}
	if err := h.schedStore.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	return &gen.ScheduledActionsCreateCreated{
		Data: gen.ScheduledActionsCreateCreatedData{ID: a.ID},
	}, nil
}

func (h *AdminHandler) ScheduledActionsUpdate(ctx context.Context, req *gen.UpdateActionRequest, params gen.ScheduledActionsUpdateParams) (*gen.OkResponse, error) {
	fields := actionDom.UpdateFields{}

	if v, ok := req.ChannelID.Get(); ok {
		fields.ChannelID = &v
	}
	if v, ok := req.Content.Get(); ok {
		fields.Content = &v
	}
	if v, ok := req.Mode.Get(); ok {
		s := string(v)
		fields.Mode = &s
	}
	if v, ok := req.ScheduledAt.Get(); ok {
		fields.ScheduledAt = &v
	}
	if v, ok := req.CronExpr.Get(); ok {
		fields.CronExpr = &v
	}
	if v, ok := req.Status.Get(); ok {
		fields.Status = &v
	}

	if err := h.schedStore.Update(ctx, params.ID, fields); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	return &gen.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) ScheduledActionsDelete(ctx context.Context, params gen.ScheduledActionsDeleteParams) (*gen.OkResponse, error) {
	if err := h.schedStore.Delete(ctx, params.ID); err != nil {
		return nil, fmt.Errorf("not found")
	}
	return &gen.OkResponse{Ok: true}, nil
}
