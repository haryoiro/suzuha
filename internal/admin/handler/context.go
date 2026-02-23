package handler

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ContextHandler proxies the agent's in-memory context.
type ContextHandler struct {
	agentURL string
	client   *http.Client
	logger   *slog.Logger
}

// NewContextHandler creates a new ContextHandler.
func NewContextHandler(agentURL string, logger *slog.Logger) *ContextHandler {
	return &ContextHandler{
		agentURL: agentURL,
		client:   &http.Client{Timeout: 5 * time.Second},
		logger:   logger,
	}
}

// Proxy forwards the context request to the agent's internal endpoint.
func (h *ContextHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentURL)
	if err != nil {
		h.logger.Error("proxy context", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
