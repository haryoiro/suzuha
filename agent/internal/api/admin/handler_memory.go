package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

func memToAPI(m memory.Memory) gen.Memory {
	am := gen.Memory{
		ID:        m.ID,
		Type:      string(m.Type),
		Content:   m.Content,
		Keywords:  m.Keywords,
		Persons:   m.Persons,
		CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: m.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if m.Topic != "" {
		am.Topic = gen.NewOptString(m.Topic)
	}
	if m.EventTime != nil {
		am.EventTime = gen.NewOptString(m.EventTime.Format("2006-01-02 15:04:05"))
	}
	if m.Metadata != nil {
		meta := make(gen.MemoryMetadata, len(m.Metadata))
		for k, v := range m.Metadata {
			b, err := json.Marshal(v)
			if err != nil {
				slog.Warn("メタデータのシリアライズに失敗", "key", k, "error", err)
				continue
			}
			meta[k] = jx.Raw(b)
		}
		am.Metadata = gen.NewOptMemoryMetadata(meta)
	}
	return am
}

func metadataFromAPI(m gen.MemoryMetadata) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			slog.Warn("メタデータのデシリアライズに失敗", "key", k, "error", err)
			continue
		}
		result[k] = val
	}
	return result
}

func (h *AdminHandler) MemoriesList(ctx context.Context, params gen.MemoriesListParams) (*gen.MemoriesListOK, error) {
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
		h.logger.Error("memories list に失敗", "error", err)
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.Memory, len(memories))
	for i, m := range memories {
		data[i] = memToAPI(m)
	}
	return &gen.MemoriesListOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) MemoriesCreate(ctx context.Context, req *gen.CreateMemoryRequest) (*gen.MemoriesCreateCreated, error) {
	var metadata map[string]any
	if meta, ok := req.Metadata.Get(); ok {
		metadata = metadataFromAPI(gen.MemoryMetadata(meta))
	}

	mem := &memory.Memory{
		Type:     memory.MemoryType(req.Type),
		Content:  req.Content,
		Metadata: metadata,
	}
	if err := h.memStore.Save(ctx, mem); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	return &gen.MemoriesCreateCreated{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesGet(ctx context.Context, params gen.MemoriesGetParams) (*gen.MemoriesGetOK, error) {
	mem, err := h.memStore.Get(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("not found")
	}
	if mem == nil {
		return nil, fmt.Errorf("not found")
	}
	return &gen.MemoriesGetOK{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesUpdate(ctx context.Context, req *gen.UpdateMemoryRequest, params gen.MemoriesUpdateParams) (*gen.MemoriesUpdateOK, error) {
	mem := &memory.Memory{ID: params.ID}
	if v, ok := req.Type.Get(); ok {
		mem.Type = memory.MemoryType(v)
	}
	if v, ok := req.Content.Get(); ok {
		mem.Content = v
	}
	if meta, ok := req.Metadata.Get(); ok {
		mem.Metadata = metadataFromAPI(gen.MemoryMetadata(meta))
	}

	if err := h.memStore.Update(ctx, mem); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	return &gen.MemoriesUpdateOK{Data: memToAPI(*mem)}, nil
}

func (h *AdminHandler) MemoriesDelete(ctx context.Context, params gen.MemoriesDeleteParams) error {
	if err := h.memStore.Delete(ctx, params.ID); err != nil {
		return fmt.Errorf("not found")
	}
	return nil
}

func (h *AdminHandler) MemoriesVecStats(ctx context.Context) (*gen.VecStats, error) {
	total, embedded, err := h.memStore.VecStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}
	return &gen.VecStats{
		TotalMemories: int32(total),
		EmbeddedCount: int32(embedded),
		MissingCount:  int32(total - embedded),
		CoveragePct:   safePct(embedded, total),
	}, nil
}

func (h *AdminHandler) MemoriesListWithVec(ctx context.Context, params gen.MemoriesListWithVecParams) (*gen.MemoriesListWithVecOK, error) {
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
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.Memory, len(memories))
	for i, m := range memories {
		data[i] = memToAPI(m)
	}

	return &gen.MemoriesListWithVecOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) MemoriesDuplicates(ctx context.Context, params gen.MemoriesDuplicatesParams) (*gen.MemoriesDuplicatesOK, error) {
	threshold := params.Threshold.Or(0.20)

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, type, content, metadata, created_at, updated_at
		 FROM memories
		 WHERE embedding IS NOT NULL
		 ORDER BY type, updated_at DESC`)
	if err != nil {
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

	var groups []gen.DuplicateGroup
	for i := range all {
		if all[i].visited {
			continue
		}
		neighRows, err := h.db.QueryContext(ctx,
			`SELECT m2.id, m1.embedding <=> m2.embedding AS dist
			 FROM memories m1
			 JOIN memories m2 ON m1.id != m2.id AND m2.embedding IS NOT NULL
			 WHERE m1.id = $1
			 ORDER BY dist LIMIT 10`, all[i].ID)
		if err != nil {
			continue
		}

		group := []gen.Memory{memToAPI(all[i].Memory)}
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
			groups = append(groups, gen.DuplicateGroup{Memories: group})
		}
	}

	if groups == nil {
		groups = []gen.DuplicateGroup{}
	}
	return &gen.MemoriesDuplicatesOK{Data: groups, Total: int32(len(groups))}, nil
}
