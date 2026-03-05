package handler

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// AgentHandler proxies operational commands to the agent's internal HTTP server.
type AgentHandler struct {
	baseURL string // e.g. "http://agent:9090"
	client  *http.Client
	logger  *slog.Logger
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(baseURL string, logger *slog.Logger) *AgentHandler {
	return &AgentHandler{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 5 * time.Minute},
		logger:  logger,
	}
}

// Compact triggers context compaction on the agent.
func (h *AgentHandler) Compact(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.baseURL+"/internal/compact", nil)
	if err != nil {
		h.logger.Error("コンパクトリクエストの作成に失敗", "error", err)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("コンパクトのプロキシに失敗", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
