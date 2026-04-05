package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/memory"
)

func memToAPI(m memory.Memory) api.Memory {
	am := api.Memory{
		ID:        m.ID,
		Type:      string(m.Type),
		Content:   m.Content,
		Keywords:  m.Keywords,
		Persons:   m.Persons,
		CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: m.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if m.Topic != "" {
		am.Topic = api.NewOptString(m.Topic)
	}
	if m.EventTime != nil {
		am.EventTime = api.NewOptString(m.EventTime.Format("2006-01-02 15:04:05"))
	}
	if m.Metadata != nil {
		meta := make(api.MemoryMetadata, len(m.Metadata))
		for k, v := range m.Metadata {
			b, _ := json.Marshal(v)
			meta[k] = jx.Raw(b)
		}
		am.Metadata = api.NewOptMemoryMetadata(meta)
	}
	return am
}

func metadataFromAPI(m api.MemoryMetadata) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		var val any
		_ = json.Unmarshal(v, &val)
		result[k] = val
	}
	return result
}

func (h *AdminHandler) MemoriesList(ctx context.Context, params api.MemoriesListParams) (*api.MemoriesListOK, error) {
	limit := int(params.Limit.Or(20))
	offset := int(params.Offset.Or(0))

	opts := memory.ListOpts{
		Offset:   offset,
		Limit:    limit,
		Type:     memory.MemoryType(params.Type.Or("")),
		Query:    params.Q.Or(""),
		OrderBy:  params.Order.Or(""),
		OrderDir: params.Dir.Or(""),
	}

	memories, total, err := h.memStore.List(ctx, opts)
	if err != nil {
		h.logger.Error("メモリ一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Memory, len(memories))
	for i, m := range memories {
		data[i] = memToAPI(m)
	}
	return &api.MemoriesListOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) MemoriesCreate(ctx context.Context, req *api.CreateMemoryRequest) (*api.MemoriesCreateCreated, error) {
	var metadata map[string]any
	if meta, ok := req.Metadata.Get(); ok {
		metadata = metadataFromAPI(api.MemoryMetadata(meta))
	}

	mem := &memory.Memory{
		Type:     memory.MemoryType(req.Type),
		Content:  req.Content,
		Metadata: metadata,
	}
	if err := h.memStore.Save(ctx, mem); err != nil {
		h.logger.Error("メモリの作成に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.MemoriesCreateCreated{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesGet(ctx context.Context, params api.MemoriesGetParams) (*api.MemoriesGetOK, error) {
	mem, err := h.memStore.Get(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("not found")
	}
	return &api.MemoriesGetOK{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesUpdate(ctx context.Context, req *api.UpdateMemoryRequest, params api.MemoriesUpdateParams) (*api.MemoriesUpdateOK, error) {
	mem := &memory.Memory{ID: params.ID}
	if v, ok := req.Type.Get(); ok {
		mem.Type = memory.MemoryType(v)
	}
	if v, ok := req.Content.Get(); ok {
		mem.Content = v
	}
	if meta, ok := req.Metadata.Get(); ok {
		mem.Metadata = metadataFromAPI(api.MemoryMetadata(meta))
	}

	if err := h.memStore.Update(ctx, mem); err != nil {
		h.logger.Error("メモリの更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.MemoriesUpdateOK{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesDelete(ctx context.Context, params api.MemoriesDeleteParams) error {
	if err := h.memStore.Delete(ctx, params.ID); err != nil {
		h.logger.Error("メモリの削除に失敗", "error", err.Error())
		return fmt.Errorf("not found")
	}
	return nil
}

func (h *AdminHandler) MemoriesVecStats(ctx context.Context) (*api.VecStats, error) {
	total, embedded, err := h.memStore.VecStats(ctx)
	if err != nil {
		h.logger.Error("ベクトル統計の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.VecStats{
		TotalMemories: int32(total),
		EmbeddedCount: int32(embedded),
		MissingCount:  int32(total - embedded),
		CoveragePct:   safePct(embedded, total),
	}, nil
}

func (h *AdminHandler) MemoriesListWithVec(ctx context.Context, params api.MemoriesListWithVecParams) (*api.MemoriesListWithVecOK, error) {
	limit := int(params.Limit.Or(20))
	offset := int(params.Offset.Or(0))

	opts := memory.ListOpts{
		Offset:   offset,
		Limit:    limit,
		Type:     memory.MemoryType(params.Type.Or("")),
		Query:    params.Q.Or(""),
		OrderBy:  params.Order.Or(""),
		OrderDir: params.Dir.Or(""),
	}

	memories, total, err := h.memStore.List(ctx, opts)
	if err != nil {
		h.logger.Error("ベクトル付きメモリ一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Memory, len(memories))
	for i, m := range memories {
		data[i] = memToAPI(m)
	}

	return &api.MemoriesListWithVecOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) MemoriesDuplicates(ctx context.Context, params api.MemoriesDuplicatesParams) (*api.MemoriesDuplicatesOK, error) {
	threshold := params.Threshold.Or(0.20)

	rows, err := h.db.QueryContext(ctx,
		`SELECT v.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.updated_at DESC`)
	if err != nil {
		h.logger.Error("重複メモリ一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	type memEntry struct {
		memory.Memory
		visited bool
	}
	var all []memEntry
	for rows.Next() {
		var e memEntry
		var metaJSON string
		if err := rows.Scan(&e.ID, &e.Type, &e.Content, &metaJSON, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		if metaJSON != "" {
			json.Unmarshal([]byte(metaJSON), &e.Metadata)
		}
		all = append(all, e)
	}

	var groups []api.DuplicateGroup
	for i := range all {
		if all[i].visited {
			continue
		}
		neighRows, err := h.db.QueryContext(ctx,
			`SELECT v2.id, v2.distance
			 FROM memories_vec v1
			 JOIN memories_vec v2 ON v2.embedding MATCH v1.embedding AND v2.k = 10
			 WHERE v1.id = $1`, all[i].ID)
		if err != nil {
			continue
		}

		group := []api.Memory{memToAPI(all[i].Memory)}
		all[i].visited = true

		for neighRows.Next() {
			var nid string
			var dist float32
			if err := neighRows.Scan(&nid, &dist); err != nil {
				continue
			}
			if nid == all[i].ID || float64(dist) >= threshold {
				continue
			}
			for j := range all {
				if all[j].ID == nid && !all[j].visited && all[j].Type == all[i].Type {
					group = append(group, memToAPI(all[j].Memory))
					all[j].visited = true
					break
				}
			}
		}
		neighRows.Close()

		if len(group) > 1 {
			groups = append(groups, api.DuplicateGroup{Memories: group})
		}
	}

	if groups == nil {
		groups = []api.DuplicateGroup{}
	}
	return &api.MemoriesDuplicatesOK{Data: groups, Total: int32(len(groups))}, nil
}

// used to suppress unused import warning
var _ = strconv.Itoa
