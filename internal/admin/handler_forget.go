package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/memory"
)

func (h *AdminHandler) ForgetGroups(ctx context.Context, params api.ForgetGroupsParams) (*api.ForgetGroupsOK, error) {
	threshold := params.Threshold.Or(0.3)

	type entry struct {
		id        string
		memType   string
		content   string
		metadata  map[string]any
		createdAt string
		updatedAt string
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT v.id, m.type, m.content, m.metadata, m.created_at, m.updated_at
		 FROM memories_vec v JOIN memories m ON m.id = v.id
		 ORDER BY m.type, m.created_at`)
	if err != nil {
		h.logger.Error("忘却: メモリ一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	defer rows.Close()

	var all []entry
	for rows.Next() {
		var e entry
		var metaJSON sql.NullString
		if err := rows.Scan(&e.id, &e.memType, &e.content, &metaJSON, &e.createdAt, &e.updatedAt); err != nil {
			continue
		}
		if metaJSON.Valid && metaJSON.String != "" {
			_ = json.Unmarshal([]byte(metaJSON.String), &e.metadata)
		}
		all = append(all, e)
	}

	if len(all) < 2 {
		return &api.ForgetGroupsOK{Data: []api.ForgetGroup{}, Total: 0}, nil
	}

	idxByID := make(map[string]int, len(all))
	for i, e := range all {
		idxByID[e.id] = i
	}

	// Union-Find.
	parent := make([]int, len(all))
	rank := make([]int, len(all))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(x, y int) {
		rx, ry := find(x), find(y)
		if rx == ry {
			return
		}
		if rank[rx] < rank[ry] {
			rx, ry = ry, rx
		}
		parent[ry] = rx
		if rank[rx] == rank[ry] {
			rank[rx]++
		}
	}

	type distPair struct {
		i, j int
		d    float64
	}
	var pairs []distPair

	for i, e := range all {
		neighRows, err := h.db.QueryContext(ctx,
			`SELECT v2.id, v2.distance
			 FROM memories_vec v1
			 JOIN memories_vec v2 ON v2.embedding MATCH v1.embedding AND v2.k = 10
			 WHERE v1.id = ?`, e.id)
		if err != nil {
			continue
		}
		for neighRows.Next() {
			var nid string
			var dist float64
			if err := neighRows.Scan(&nid, &dist); err != nil {
				continue
			}
			if nid == e.id || dist >= threshold {
				continue
			}
			j, ok := idxByID[nid]
			if !ok || all[i].memType != all[j].memType {
				continue
			}
			union(i, j)
			pairs = append(pairs, distPair{i, j, dist})
		}
		neighRows.Close()
	}

	groupMap := make(map[int][]int)
	for i := range all {
		root := find(i)
		groupMap[root] = append(groupMap[root], i)
	}

	groupDists := make(map[int][]float64)
	for _, p := range pairs {
		root := find(p.i)
		groupDists[root] = append(groupDists[root], p.d)
	}

	var groups []api.ForgetGroup
	for root, members := range groupMap {
		if len(members) < 2 {
			continue
		}
		g := api.ForgetGroup{Type: all[members[0]].memType}
		for _, idx := range members {
			e := all[idx]
			g.Members = append(g.Members, api.Memory{
				ID:        e.id,
				Type:      e.memType,
				Content:   e.content,
				CreatedAt: e.createdAt,
				UpdatedAt: e.updatedAt,
			})
		}
		if dists := groupDists[root]; len(dists) > 0 {
			var sum float64
			for _, d := range dists {
				sum += d
			}
			g.AvgDistance = sum / float64(len(dists))
		}
		groups = append(groups, g)
	}

	if groups == nil {
		groups = []api.ForgetGroup{}
	}
	return &api.ForgetGroupsOK{Data: groups, Total: int32(len(groups))}, nil
}

func (h *AdminHandler) ForgetDelete(ctx context.Context, req *api.ForgetDeleteRequest) (*api.ForgetDeleteOK, error) {
	deleted, err := h.memStore.DeleteBatch(ctx, req.DeleteIds)
	if err != nil {
		h.logger.Error("忘却: 削除に失敗", "error", err.Error())
		return nil, fmt.Errorf("delete failed")
	}
	h.logger.Info("忘却: 手動削除を実行", "requested", len(req.DeleteIds), "deleted", deleted)
	return &api.ForgetDeleteOK{Deleted: int32(deleted)}, nil
}

func (h *AdminHandler) ForgetMerge(ctx context.Context, req *api.ForgetMergeRequest) (*api.ForgetMergeOK, error) {
	deleted, err := h.memStore.DeleteBatch(ctx, req.DeleteIds)
	if err != nil {
		h.logger.Error("忘却: マージ時の削除に失敗", "error", err.Error())
	}

	mem := &memory.Memory{
		Type:    memory.MemoryType(req.Type),
		Content: req.MergedContent,
		Metadata: map[string]any{
			"source": "forget_merge",
		},
	}
	if saveErr := h.memStore.SaveRaw(ctx, mem); saveErr != nil {
		h.logger.Error("忘却: マージ後の挿入に失敗", "error", saveErr.Error())
		return nil, fmt.Errorf("merge insert failed")
	}

	h.logger.Info("忘却: 手動マージを実行", "deleted", deleted, "merged_content_len", len(req.MergedContent))
	return &api.ForgetMergeOK{Deleted: int32(deleted), Merged: true}, nil
}
