package handler

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// PlaygroundHandler proxies playground chat requests to the agent.
type PlaygroundHandler struct {
	agentBase string
	client    *http.Client
	logger    *slog.Logger
}

// NewPlaygroundHandler creates a new PlaygroundHandler.
func NewPlaygroundHandler(agentBase string, logger *slog.Logger) *PlaygroundHandler {
	return &PlaygroundHandler{
		agentBase: agentBase,
		client:    &http.Client{Timeout: 2 * time.Minute},
		logger:    logger,
	}
}

// Send proxies POST /api/playground to the agent's /internal/playground.
func (h *PlaygroundHandler) Send(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.agentBase+"/internal/playground", r.Body)
	if err != nil {
		http.Error(w, `{"error":"request build failed"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Error("プレイグラウンドのプロキシに失敗", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
