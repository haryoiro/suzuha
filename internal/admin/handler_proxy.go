package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/go-faster/jx"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

// proxyGet performs a GET proxy to the agent and returns raw JSON.
func (h *AdminHandler) proxyGet(ctx context.Context, path string) (jx.Raw, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.agentBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("proxy request failed: %w", err)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("プロキシに失敗", "path", path, "error", err.Error())
		return nil, fmt.Errorf("agent unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return jx.Raw(body), nil
}

// proxyPostRaw performs a POST proxy with optional body.
func (h *AdminHandler) proxyPostRaw(ctx context.Context, path string, reqBody io.Reader) (jx.Raw, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.agentBase+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("proxy request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.longClient.Do(req)
	if err != nil {
		h.logger.Error("プロキシに失敗", "path", path, "error", err.Error())
		return nil, fmt.Errorf("agent unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return jx.Raw(body), nil
}

// proxyPutRaw performs a PUT proxy with body.
func (h *AdminHandler) proxyPutRaw(ctx context.Context, path string, reqBody io.Reader) (jx.Raw, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, h.agentBase+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("proxy request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("プロキシに失敗", "path", path, "error", err.Error())
		return nil, fmt.Errorf("agent unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return jx.Raw(body), nil
}

func (h *AdminHandler) AgentCompact(ctx context.Context) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/compact", nil)
}

func (h *AdminHandler) ContextGet(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/context")
}

func (h *AdminHandler) IdentityGet(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/identity")
}

func (h *AdminHandler) ToolsList(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/tools")
}

func (h *AdminHandler) ToolsSetEnabled(ctx context.Context, req jx.Raw, params api.ToolsSetEnabledParams) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/tools/"+params.Name+"/enabled", jxReader(req))
}

func (h *AdminHandler) LLMGet(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/llm")
}

func (h *AdminHandler) LLMUpdate(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/llm", jxReader(req))
}

func (h *AdminHandler) PlaygroundChat(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/playground", jxReader(req))
}

func (h *AdminHandler) ForgetRun(ctx context.Context) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/trigger/forget", nil)
}

func (h *AdminHandler) ForgetStatus(ctx context.Context) (jx.Raw, error) {
	var stateJSON string
	err := h.db.QueryRowContext(ctx,
		`SELECT state FROM task_state WHERE task_name = 'forget'`,
	).Scan(&stateJSON)
	if err != nil {
		return jx.Raw(`{"has_run":false}`), nil
	}
	return jx.Raw(stateJSON), nil
}

// notifyAgentReload tells the agent to reload cached data.
func (h *AdminHandler) notifyAgentReload(ctx context.Context, path string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.agentBase+path, nil)
	if err != nil {
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Warn("再読み込みプロキシに失敗", "path", path, "error", err.Error())
		return
	}
	resp.Body.Close()
}
