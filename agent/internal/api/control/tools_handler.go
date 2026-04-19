package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/tool"
	"github.com/samber/do/v2"
)

// ToolsHandler は Tools グループ (list / enable / execute) を実装する。
type ToolsHandler struct {
	registry *tool.Registry
	db       *sql.DB // disabled tools の永続化に使う
}

// NewToolsHandler は DI injector から依存を取り出して ToolsHandler を生成する。
func NewToolsHandler(i do.Injector) (gen.ToolsHandler, error) {
	return &ToolsHandler{
		registry: do.MustInvoke[*tool.Registry](i),
		db:       do.MustInvokeNamed[*sql.DB](i, "shared-db"),
	}, nil
}

// ToolsList implements GET /internal/tools.
func (h *ToolsHandler) ToolsList(ctx context.Context) (*gen.ToolsListResponse, error) {
	tools := h.registry.All()
	out := make([]gen.ToolInfo, 0, len(tools))
	for _, t := range tools {
		schema := gen.ToolInfoInputSchema{}
		if raw := t.InputSchema(); len(raw) > 0 {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err == nil {
				for k, v := range m {
					schema[k] = jx.Raw(v)
				}
			}
		}
		out = append(out, gen.ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: schema,
			Enabled:     !h.registry.IsDisabled(t.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return &gen.ToolsListResponse{Data: out}, nil
}

// ToolsSetEnabled implements PUT /internal/tools/{name}/enabled.
func (h *ToolsHandler) ToolsSetEnabled(ctx context.Context, req *gen.SetToolEnabledRequest, params gen.ToolsSetEnabledParams) (*gen.OkResponse, error) {
	h.registry.SetEnabled(params.Name, req.Enabled)
	if err := tool.SaveDisabled(ctx, h.db, h.registry.DisabledNames()); err != nil {
		return nil, err
	}
	return &gen.OkResponse{Ok: true}, nil
}

// ToolsExecute implements POST /internal/tools/{name}/execute.
func (h *ToolsHandler) ToolsExecute(ctx context.Context, req gen.ToolsExecuteReq, params gen.ToolsExecuteParams) (*gen.ToolExecuteResponse, error) {
	t, ok := h.registry.Get(params.Name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", params.Name)
	}
	input := []byte("{}")
	if len(req) > 0 {
		m := make(map[string]json.RawMessage, len(req))
		for k, v := range req {
			m[k] = json.RawMessage(v)
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		input = b
	}
	result, err := t.Execute(ctx, json.RawMessage(input))
	if err != nil {
		return nil, err
	}
	var text string
	for _, c := range result.Content {
		text += c.Text
	}
	return &gen.ToolExecuteResponse{
		Ok:      !result.IsError,
		Output:  text,
		IsError: result.IsError,
	}, nil
}
