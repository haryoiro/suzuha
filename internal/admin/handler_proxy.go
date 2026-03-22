package admin

import (
	"bytes"
	"context"
	"encoding/json"
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

func (h *AdminHandler) ContextGet(ctx context.Context, params api.ContextGetParams) (jx.Raw, error) {
	path := "/internal/context"
	if src, ok := params.Source.Get(); ok {
		path += "?source=" + string(src)
	}
	return h.proxyGet(ctx, path)
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

func (h *AdminHandler) ForgetRun(ctx context.Context, req api.OptForgetRunReq) (jx.Raw, error) {
	var body io.Reader
	if req.IsSet() {
		if st, ok := req.Value.GetSimilarityThreshold().Get(); ok {
			cfg := map[string]any{"config": map[string]any{"similarity_threshold": st}}
			data, _ := json.Marshal(cfg)
			body = bytes.NewReader(data)
		}
	}
	return h.proxyPostRaw(ctx, "/internal/trigger/forget", body)
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

func (h *AdminHandler) proxySchedulerJobs(w http.ResponseWriter, r *http.Request) {
	data, err := h.proxyGet(r.Context(), "/internal/scheduler/jobs")
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *AdminHandler) proxySchedulerTrigger(w http.ResponseWriter, r *http.Request) {
	task := r.PathValue("task")
	data, err := h.proxyPostRaw(r.Context(), "/internal/trigger/"+task, r.Body)
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *AdminHandler) proxyVoicevoxSpeakers(w http.ResponseWriter, r *http.Request) {
	data, err := h.proxyGet(r.Context(), "/internal/voicevox/speakers")
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *AdminHandler) proxyVoicevoxCurrentSpeaker(w http.ResponseWriter, r *http.Request) {
	data, err := h.proxyGet(r.Context(), "/internal/voicevox/speaker")
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *AdminHandler) proxyVoicevoxSetSpeaker(w http.ResponseWriter, r *http.Request) {
	data, err := h.proxyPutRaw(r.Context(), "/internal/voicevox/speaker", r.Body)
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// proxyDeviceFrame proxies the latest camera frame from the internal server.
func (h *AdminHandler) proxyDeviceFrame(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.agentBase+"/internal/device/frame", nil)
	if err != nil {
		http.Error(w, "proxy error", http.StatusInternalServerError)
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, "agent unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// proxyDeviceDetections proxies the SSE detection stream from the internal server.
func (h *AdminHandler) proxyDeviceDetections(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.agentBase+"/internal/device/detections", nil)
	if err != nil {
		http.Error(w, "proxy error", http.StatusInternalServerError)
		return
	}
	resp, err := h.longClient.Do(req)
	if err != nil {
		http.Error(w, "agent unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
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
