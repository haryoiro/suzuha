package handler

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// IdentityHandler proxies bot identity requests to the agent.
type IdentityHandler struct {
	agentBase string
	client    *http.Client
	logger    *slog.Logger
}

// NewIdentityHandler creates a new IdentityHandler.
func NewIdentityHandler(agentBase string, logger *slog.Logger) *IdentityHandler {
	return &IdentityHandler{
		agentBase: agentBase,
		client:    &http.Client{Timeout: 5 * time.Second},
		logger:    logger,
	}
}

// Get proxies GET /api/identity to the agent's /internal/identity.
func (h *IdentityHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentBase + "/internal/identity")
	if err != nil {
		h.logger.Error("アイデンティティのプロキシに失敗", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
