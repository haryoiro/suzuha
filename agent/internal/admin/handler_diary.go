package admin

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
)

func (h *AdminHandler) DiaryList(ctx context.Context, params api.DiaryListParams) (*api.DiaryListOK, error) {
	limit := int(params.Limit.Or(50))
	offset := int(params.Offset.Or(0))
	kind := params.Kind.Or("")

	// kind が指定されていない場合は全件取得。
	var since time.Time // ゼロ値 = 全期間
	entries, err := h.diaryStore.ListByKind(ctx, kind, since, limit+offset)
	if err != nil {
		return nil, err
	}

	// 手動 offset（DiaryStore に offset がないため）。
	if offset > 0 && offset < len(entries) {
		entries = entries[offset:]
	} else if offset >= len(entries) {
		entries = nil
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}

	loc := jtime.Location()
	data := make([]api.DiaryEntry, len(entries))
	for i, e := range entries {
		data[i] = api.DiaryEntry{
			ID:          e.ID,
			Kind:        e.Kind,
			Content:     e.Content,
			PeriodStart: e.PeriodStart.In(loc).Format(time.RFC3339),
			PeriodEnd:   e.PeriodEnd.In(loc).Format(time.RFC3339),
			CreatedAt:   e.CreatedAt.In(loc).Format(time.RFC3339),
		}
	}

	return &api.DiaryListOK{Data: data, Total: int32(len(data))}, nil
}
