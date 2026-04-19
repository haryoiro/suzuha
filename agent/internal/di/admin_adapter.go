package di

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/feature/action"
	"github.com/haryoiro/suzuha/internal/feature/diary"
)

// actionStoreAdapter は action.Store を admin.ActionStore に適合させる。
type actionStoreAdapter struct {
	s *action.Store
}

func (a *actionStoreAdapter) List(ctx context.Context, opts admin.ActionListOpts) ([]admin.Action, error) {
	actions, err := a.s.List(ctx, action.ActionListOpts{
		Status: opts.Status,
		Limit:  opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]admin.Action, len(actions))
	for i, act := range actions {
		out[i] = toAdminAction(act)
	}
	return out, nil
}

func (a *actionStoreAdapter) Create(ctx context.Context, act *admin.Action) error {
	fa := &action.Action{
		ID:          act.ID,
		ChannelID:   act.ChannelID,
		Content:     act.Content,
		Mode:        act.Mode,
		ScheduledAt: act.ScheduledAt,
		CronExpr:    act.CronExpr,
		CreatedBy:   act.CreatedBy,
	}
	if err := a.s.Create(ctx, fa); err != nil {
		return err
	}
	act.ID = fa.ID
	return nil
}

func (a *actionStoreAdapter) Update(ctx context.Context, id string, fields admin.ActionUpdateFields) error {
	return a.s.Update(ctx, id, action.ActionUpdateFields{
		ChannelID:   fields.ChannelID,
		Content:     fields.Content,
		Mode:        fields.Mode,
		ScheduledAt: fields.ScheduledAt,
		CronExpr:    fields.CronExpr,
		Status:      fields.Status,
	})
}

func (a *actionStoreAdapter) Delete(ctx context.Context, id string) error {
	return a.s.Delete(ctx, id)
}

func toAdminAction(a action.Action) admin.Action {
	return admin.Action{
		ID:            a.ID,
		ChannelID:     a.ChannelID,
		Content:       a.Content,
		Mode:          a.Mode,
		ScheduledAt:   a.ScheduledAt,
		CronExpr:      a.CronExpr,
		RandomMinutes: a.RandomMinutes,
		CreatedBy:     a.CreatedBy,
		Status:        a.Status,
		RetryCount:    a.RetryCount,
		ExecutedAt:    a.ExecutedAt,
		CreatedAt:     a.CreatedAt,
	}
}

// diaryStoreAdapter は diary.Store を admin.DiaryStore に適合させる。
type diaryStoreAdapter struct {
	s *diary.Store
}

func (a *diaryStoreAdapter) ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]admin.DiaryEntry, error) {
	entries, err := a.s.ListByKind(ctx, kind, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.DiaryEntry, len(entries))
	for i, e := range entries {
		out[i] = admin.DiaryEntry{
			ID:          e.ID,
			Kind:        e.Kind,
			Content:     e.Content,
			PeriodStart: e.PeriodStart,
			PeriodEnd:   e.PeriodEnd,
			CreatedAt:   e.CreatedAt,
		}
	}
	return out, nil
}

