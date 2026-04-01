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

// proxyDeleteRaw performs a DELETE proxy.
func (h *AdminHandler) proxyDeleteRaw(ctx context.Context, path string) (jx.Raw, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.agentBase+path, nil)
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

// --- LLM Preset / Assignment (ogen interface) ---

func (h *AdminHandler) LLMPresetsList(ctx context.Context) ([]api.LLMPreset, error) {
	data, err := h.proxyGet(ctx, "/internal/llm/presets")
	if err != nil {
		return nil, err
	}
	var presets []api.LLMPreset
	if err := json.Unmarshal(data, &presets); err != nil {
		return nil, err
	}
	return presets, nil
}

func (h *AdminHandler) LLMPresetsCreate(ctx context.Context, req *api.LLMPreset) (*api.OkResponse, error) {
	body, _ := json.Marshal(req)
	if _, err := h.proxyPostRaw(ctx, "/internal/llm/presets", bytes.NewReader(body)); err != nil {
		return nil, err
	}
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LLMPresetsUpdate(ctx context.Context, req *api.LLMPreset, params api.LLMPresetsUpdateParams) (*api.OkResponse, error) {
	body, _ := json.Marshal(req)
	if _, err := h.proxyPutRaw(ctx, "/internal/llm/presets/"+params.Name, bytes.NewReader(body)); err != nil {
		return nil, err
	}
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LLMPresetsDelete(ctx context.Context, params api.LLMPresetsDeleteParams) (*api.OkResponse, error) {
	if _, err := h.proxyDeleteRaw(ctx, "/internal/llm/presets/"+params.Name); err != nil {
		return nil, err
	}
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LLMAssignmentsList(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/llm/assignments")
}

func (h *AdminHandler) LLMAssignmentsUpdate(ctx context.Context, req *api.LLMAssignmentsUpdateReq, params api.LLMAssignmentsUpdateParams) (*api.OkResponse, error) {
	body, _ := json.Marshal(req)
	if _, err := h.proxyPutRaw(ctx, "/internal/llm/assignments/"+params.Role, bytes.NewReader(body)); err != nil {
		return nil, err
	}
	return &api.OkResponse{Ok: true}, nil
}

// --- Scheduler (ogen interface) ---

func (h *AdminHandler) SchedulerJobs(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/scheduler/jobs")
}

func (h *AdminHandler) SchedulerTrigger(ctx context.Context, req jx.Raw, params api.SchedulerTriggerParams) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/trigger/"+params.Task, jxReader(req))
}

// --- Tools execute (ogen interface) ---

func (h *AdminHandler) ToolsExecute(ctx context.Context, req jx.Raw, params api.ToolsExecuteParams) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/tools/"+params.Name+"/execute", jxReader(req))
}

// --- Device (ogen interface) ---

func (h *AdminHandler) DeviceVisionGet(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/device/vision")
}

func (h *AdminHandler) DeviceVisionSet(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/device/vision", jxReader(req))
}

func (h *AdminHandler) DeviceServo(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPostRaw(ctx, "/internal/device/servo", jxReader(req))
}

func (h *AdminHandler) DeviceVolume(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/device/volume", jxReader(req))
}

func (h *AdminHandler) DeviceTrackerGet(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/device/tracker")
}

func (h *AdminHandler) DeviceTrackerSet(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/device/tracker", jxReader(req))
}

// --- Voicevox (ogen interface) ---

func (h *AdminHandler) VoicevoxSpeakers(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/voicevox/speakers")
}

func (h *AdminHandler) VoicevoxCurrentSpeaker(ctx context.Context) (jx.Raw, error) {
	return h.proxyGet(ctx, "/internal/voicevox/speaker")
}

func (h *AdminHandler) VoicevoxSetSpeaker(ctx context.Context, req jx.Raw) (jx.Raw, error) {
	return h.proxyPutRaw(ctx, "/internal/voicevox/speaker", jxReader(req))
}

// --- Channels delete (ogen interface) ---

func (h *AdminHandler) ChannelsDelete(ctx context.Context, params api.ChannelsDeleteParams) (*api.OkResponse, error) {
	h.deleteChannelByID(ctx, params.ChannelId)
	return &api.OkResponse{Ok: true}, nil
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

// Note: proxySchedulerJobs, proxySchedulerTrigger, proxyToolExecute,
// proxyVoicevoxSpeakers, proxyVoicevoxCurrentSpeaker, proxyVoicevoxSetSpeaker
// are now implemented via ogen interface methods above.

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
