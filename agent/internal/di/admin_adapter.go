package di

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/api/admin"
	"github.com/haryoiro/suzuha/internal/feature/action"
	"github.com/haryoiro/suzuha/internal/feature/diary"
	"github.com/haryoiro/suzuha/internal/feature/location"
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

// locationStoreAdapter は location.Store を admin.LocationStore に適合させる。
type locationStoreAdapter struct {
	s *location.Store
}

func (a *locationStoreAdapter) QueryLatestByUserID(ctx context.Context, userID string) ([]admin.UserLocation, error) {
	locs, err := a.s.QueryLatestByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]admin.UserLocation, len(locs))
	for i, l := range locs {
		ul := admin.UserLocation{PlaceName: l.PlaceName}
		if l.Location != nil {
			ul.Location = &admin.Location{
				Latitude:           l.Location.Latitude,
				Longitude:          l.Location.Longitude,
				Altitude:           l.Location.Altitude,
				Speed:              l.Location.Speed,
				HorizontalAccuracy: l.Location.HorizontalAccuracy,
				Timestamp:          l.Location.Timestamp,
			}
		}
		if l.Device != nil {
			ul.Device = &admin.DeviceMapping{
				DeviceID:  l.Device.DeviceID,
				OwnerName: l.Device.OwnerName,
				UserID:    l.Device.UserID,
				CreatedAt: l.Device.CreatedAt,
			}
		}
		out[i] = ul
	}
	return out, nil
}

func (a *locationStoreAdapter) ListDevices(ctx context.Context) ([]admin.DeviceMapping, error) {
	devices, err := a.s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.DeviceMapping, len(devices))
	for i, d := range devices {
		out[i] = admin.DeviceMapping{
			DeviceID:  d.DeviceID,
			OwnerName: d.OwnerName,
			UserID:    d.UserID,
			CreatedAt: d.CreatedAt,
		}
	}
	return out, nil
}

func (a *locationStoreAdapter) UpsertDevice(ctx context.Context, deviceID, ownerName, userID string) error {
	return a.s.UpsertDevice(ctx, deviceID, ownerName, userID)
}

func (a *locationStoreAdapter) DeleteDevice(ctx context.Context, deviceID string) error {
	return a.s.DeleteDevice(ctx, deviceID)
}

func (a *locationStoreAdapter) ListPlaces(ctx context.Context) ([]admin.Place, error) {
	places, err := a.s.ListPlaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.Place, len(places))
	for i, p := range places {
		out[i] = admin.Place{
			ID:        p.ID,
			Name:      p.Name,
			Latitude:  p.Latitude,
			Longitude: p.Longitude,
			RadiusM:   p.RadiusM,
			CreatedAt: p.CreatedAt,
		}
	}
	return out, nil
}

func (a *locationStoreAdapter) CreatePlace(ctx context.Context, p admin.Place) error {
	return a.s.CreatePlace(ctx, location.Place{
		ID:        p.ID,
		Name:      p.Name,
		Latitude:  p.Latitude,
		Longitude: p.Longitude,
		RadiusM:   p.RadiusM,
	})
}

func (a *locationStoreAdapter) UpdatePlace(ctx context.Context, p admin.Place) error {
	return a.s.UpdatePlace(ctx, location.Place{
		ID:        p.ID,
		Name:      p.Name,
		Latitude:  p.Latitude,
		Longitude: p.Longitude,
		RadiusM:   p.RadiusM,
	})
}

func (a *locationStoreAdapter) DeletePlace(ctx context.Context, id string) error {
	return a.s.DeletePlace(ctx, id)
}
