package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/robfig/cron/v3"
)

func actionToAPI(a Action) api.ScheduledAction {
	sa := api.ScheduledAction{
		ID:          a.ID,
		ChannelID:   a.ChannelID,
		Content:     a.Content,
		Mode:        api.ScheduledActionMode(a.Mode),
		ScheduledAt: a.ScheduledAt.Format(time.RFC3339),
		Status:      api.ScheduledActionStatus(a.Status),
		RetryCount:  int32(a.RetryCount),
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
	}
	if a.CronExpr != "" {
		sa.CronExpr = api.NewOptString(a.CronExpr)
	}
	if a.CreatedBy != "" {
		sa.CreatedBy = api.NewOptString(a.CreatedBy)
	}
	if a.ExecutedAt != nil {
		sa.ExecutedAt = api.NewOptString(a.ExecutedAt.Format(time.RFC3339))
	}
	return sa
}

func (h *AdminHandler) ScheduledActionsList(ctx context.Context, params api.ScheduledActionsListParams) (*api.ScheduledActionsListOK, error) {
	limit := int(params.Limit.Or(50))

	actions, err := h.schedStore.List(ctx, ActionListOpts{
		Status: params.Status.Or(""),
		Limit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.ScheduledAction, 0, len(actions))
	for _, a := range actions {
		data = append(data, actionToAPI(a))
	}
	return &api.ScheduledActionsListOK{Data: data}, nil
}

func (h *AdminHandler) ScheduledActionsCreate(ctx context.Context, req *api.CreateActionRequest) (*api.ScheduledActionsCreateCreated, error) {
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

	mode := string(req.Mode.Or(api.CreateActionRequestModePrompt))
	cronExpr := req.CronExpr.Or("")

	a := &Action{
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
	return &api.ScheduledActionsCreateCreated{
		Data: api.ScheduledActionsCreateCreatedData{ID: a.ID},
	}, nil
}

func (h *AdminHandler) ScheduledActionsUpdate(ctx context.Context, req *api.UpdateActionRequest, params api.ScheduledActionsUpdateParams) (*api.OkResponse, error) {
	fields := ActionUpdateFields{}

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
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) ScheduledActionsDelete(ctx context.Context, params api.ScheduledActionsDeleteParams) (*api.OkResponse, error) {
	if err := h.schedStore.Delete(ctx, params.ID); err != nil {
		return nil, fmt.Errorf("not found")
	}
	return &api.OkResponse{Ok: true}, nil
}
