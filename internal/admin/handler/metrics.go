// Package handler provides HTTP handlers for the admin dashboard API.
package handler

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MetricsHandler proxies and parses Prometheus metrics from the agent.
type MetricsHandler struct {
	agentURL string
	client   *http.Client
	logger   *slog.Logger
}

// NewMetricsHandler creates a new MetricsHandler.
func NewMetricsHandler(agentURL string, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{
		agentURL: agentURL,
		client:   &http.Client{Timeout: 5 * time.Second},
		logger:   logger,
	}
}

// Proxy forwards raw Prometheus metrics text from the agent.
func (h *MetricsHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentURL)
	if err != nil {
		h.logger.Error("proxy metrics", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

type metricJSON struct {
	Name   string            `json:"name"`
	Help   string            `json:"help,omitempty"`
	Type   string            `json:"type,omitempty"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

type metricsResponse struct {
	Metrics []metricJSON `json:"metrics"`
}

// ProxyJSON fetches Prometheus metrics and returns suzuha_* metrics as JSON.
// Parses the text exposition format directly to avoid prometheus/common
// validation scheme issues.
func (h *MetricsHandler) ProxyJSON(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentURL)
	if err != nil {
		h.logger.Error("proxy metrics json", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	helps := make(map[string]string)
	types := make(map[string]string)
	var result []metricJSON

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Parse HELP lines.
		if strings.HasPrefix(line, "# HELP suzuha_") {
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 4 {
				helps[parts[2]] = parts[3]
			}
			continue
		}

		// Parse TYPE lines.
		if strings.HasPrefix(line, "# TYPE suzuha_") {
			parts := strings.SplitN(line, " ", 4)
			if len(parts) >= 4 {
				types[parts[2]] = parts[3]
			}
			continue
		}

		// Skip non-suzuha lines and comments.
		if !strings.HasPrefix(line, "suzuha_") {
			continue
		}

		// Parse metric line: name{labels} value
		name, labels, valueStr := parseMetricLine(line)
		if name == "" {
			continue
		}

		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			continue
		}

		jm := metricJSON{
			Name:   name,
			Help:   helps[name],
			Type:   types[name],
			Value:  value,
			Labels: labels,
		}
		result = append(result, jm)
	}

	if result == nil {
		result = []metricJSON{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metricsResponse{Metrics: result})
}

// parseMetricLine parses "name{k1=\"v1\",k2=\"v2\"} value" or "name value".
func parseMetricLine(line string) (name string, labels map[string]string, value string) {
	// Split off value (last space-separated token).
	idx := strings.LastIndex(line, " ")
	if idx < 0 {
		return "", nil, ""
	}
	value = line[idx+1:]
	prefix := line[:idx]

	// Check for labels.
	braceStart := strings.Index(prefix, "{")
	if braceStart < 0 {
		return prefix, nil, value
	}

	name = prefix[:braceStart]
	labelsStr := prefix[braceStart+1 : len(prefix)-1] // strip { }
	labels = make(map[string]string)

	for _, pair := range splitLabels(labelsStr) {
		eqIdx := strings.Index(pair, "=")
		if eqIdx < 0 {
			continue
		}
		k := pair[:eqIdx]
		v := strings.Trim(pair[eqIdx+1:], `"`)
		labels[k] = v
	}

	return name, labels, value
}

// splitLabels splits label pairs, respecting quoted values.
func splitLabels(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	escaped := false

	for _, c := range s {
		if escaped {
			current.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuote {
			escaped = true
			current.WriteRune(c)
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			current.WriteRune(c)
			continue
		}
		if c == ',' && !inQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(c)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
