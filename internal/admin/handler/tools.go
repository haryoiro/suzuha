package handler

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ToolsHandler proxies tool registry requests to the agent.
type ToolsHandler struct {
	agentBase string
	client    *http.Client
	logger    *slog.Logger
}

// NewToolsHandler creates a new ToolsHandler.
func NewToolsHandler(agentBase string, logger *slog.Logger) *ToolsHandler {
	return &ToolsHandler{
		agentBase: agentBase,
		client:    &http.Client{Timeout: 5 * time.Second},
		logger:    logger,
	}
}

// List proxies GET /api/tools to the agent's /internal/tools endpoint.
func (h *ToolsHandler) List(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentBase + "/internal/tools")
	if err != nil {
		h.logger.Error("proxy tools", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ToggleTool proxies PUT /api/tools/{name}/enabled to the agent.
func (h *ToolsHandler) ToggleTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut,
		h.agentBase+"/internal/tools/"+name+"/enabled", r.Body)
	if err != nil {
		http.Error(w, `{"error":"request build failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("proxy tool toggle", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// LLMHandler proxies LLM provider info/swap requests to the agent.
type LLMHandler struct {
	agentBase string
	client    *http.Client
	logger    *slog.Logger
}

// NewLLMHandler creates a new LLMHandler.
func NewLLMHandler(agentBase string, logger *slog.Logger) *LLMHandler {
	return &LLMHandler{
		agentBase: agentBase,
		client:    &http.Client{Timeout: 5 * time.Second},
		logger:    logger,
	}
}

// Get proxies GET /api/llm to the agent's /internal/llm.
func (h *LLMHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentBase + "/internal/llm")
	if err != nil {
		h.logger.Error("proxy llm get", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// Put proxies PUT /api/llm to the agent's /internal/llm.
func (h *LLMHandler) Put(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut, h.agentBase+"/internal/llm", r.Body)
	if err != nil {
		http.Error(w, `{"error":"request build failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("proxy llm put", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
