package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/preferences"
)

func (h *AdminHandler) prefStore() *preferences.Store {
	return preferences.NewStore(h.db)
}

func prefToAPI(p preferences.Preference) api.Preference {
	ap := api.Preference{
		ID:         int32(p.ID),
		Category:   p.Category,
		Topic:      p.Topic,
		Stance:     api.PreferenceStance(p.Stance),
		Confidence: p.Confidence,
		Reasoning:  p.Reasoning,
		Encounters: int32(p.Encounters),
		Shared:     p.Shared,
		CreatedAt:  p.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  p.UpdatedAt.Format(time.RFC3339),
	}
	if !p.LastEvaluatedAt.IsZero() {
		ap.LastEvaluatedAt = api.NewOptString(p.LastEvaluatedAt.Format(time.RFC3339))
	}
	return ap
}

func (h *AdminHandler) PreferencesList(ctx context.Context, params api.PreferencesListParams) (*api.PreferencesListOK, error) {
	stance := params.Stance.Or("")
	store := h.prefStore()

	var prefs []preferences.Preference
	var err error
	if stance != "" && stance != "all" {
		prefs, err = store.ListByStance(ctx, preferences.Stance(stance), 100)
	} else {
		prefs, err = store.ListAll(ctx, 100)
	}
	if err != nil {
		h.logger.Error("好み一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Preference, len(prefs))
	for i, p := range prefs {
		data[i] = prefToAPI(p)
	}
	return &api.PreferencesListOK{Data: data, Total: int32(len(prefs))}, nil
}

func (h *AdminHandler) PreferencesUpdate(ctx context.Context, req *api.UpdatePreferenceRequest, params api.PreferencesUpdateParams) (*api.OkResponse, error) {
	id := int64(params.ID)

	var curStance string
	var curConfidence float64
	var curReasoning string
	err := h.db.QueryRowContext(ctx,
		`SELECT stance, confidence, reasoning FROM preferences WHERE id = ?`, id,
	).Scan(&curStance, &curConfidence, &curReasoning)
	if err != nil {
		return nil, fmt.Errorf("not found")
	}

	stance := curStance
	confidence := curConfidence
	reasoning := curReasoning
	if v, ok := req.Stance.Get(); ok {
		stance = string(v)
	}
	if v, ok := req.Confidence.Get(); ok {
		confidence = v
	}
	if v, ok := req.Reasoning.Get(); ok {
		reasoning = v
	}

	if err := h.prefStore().MarkEvaluated(ctx, id, preferences.Stance(stance), confidence, reasoning); err != nil {
		h.logger.Error("好みの更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) PreferencesDelete(ctx context.Context, params api.PreferencesDeleteParams) error {
	if err := h.prefStore().Delete(ctx, int64(params.ID)); err != nil {
		h.logger.Error("好みの削除に失敗", "error", err.Error())
		return fmt.Errorf("internal error")
	}
	return nil
}

func (h *AdminHandler) PreferencesStats(ctx context.Context) (*api.PreferenceStats, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT stance, COUNT(*) FROM preferences GROUP BY stance`)
	if err != nil {
		h.logger.Error("好み統計の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	stats := &api.PreferenceStats{}
	for rows.Next() {
		var stance string
		var count int32
		if err := rows.Scan(&stance, &count); err == nil {
			switch stance {
			case "liked":
				stats.Liked = count
			case "disliked":
				stats.Disliked = count
			case "curious":
				stats.Curious = count
			case "undecided":
				stats.Undecided = count
			}
			stats.Total += count
		}
	}
	return stats, nil
}
