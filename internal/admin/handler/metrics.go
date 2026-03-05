// Package handler provides HTTP handlers for the admin dashboard API.
package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// MetricsHandler reads metrics directly from the shared SQLite database.
type MetricsHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewMetricsHandler creates a new MetricsHandler backed by SQLite.
func NewMetricsHandler(db *sql.DB, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{db: db, logger: logger}
}

// metricHelp maps metric names to human-readable descriptions.
var metricHelp = map[string]string{
	"suzuha_llm_tokens_input_total":    "Total input tokens sent to LLM",
	"suzuha_llm_tokens_output_total":   "Total output tokens received from LLM",
	"suzuha_llm_latency_seconds":       "LLM request latency in seconds",
	"suzuha_embedding_latency_seconds": "Embedding API request latency in seconds",
	"suzuha_context_window_usage_ratio": "Current context window usage ratio (0-1)",
	"suzuha_tool_calls_total":          "Total tool calls by tool name and status",
	"suzuha_events_total":              "Total events by source and type",
	"suzuha_memory_writes_total":       "Total writes to long-term memory",
}

// metricType maps metric names to their types.
var metricType = map[string]string{
	"suzuha_llm_tokens_input_total":     "counter",
	"suzuha_llm_tokens_output_total":    "counter",
	"suzuha_llm_latency_seconds":        "histogram",
	"suzuha_embedding_latency_seconds":  "histogram",
	"suzuha_context_window_usage_ratio": "gauge",
	"suzuha_tool_calls_total":           "counter",
	"suzuha_events_total":               "counter",
	"suzuha_memory_writes_total":        "counter",
}

type metricJSON struct {
	Name    string             `json:"name"`
	Help    string             `json:"help,omitempty"`
	Type    string             `json:"type,omitempty"`
	Value   float64            `json:"value"`
	Labels  map[string]string  `json:"labels,omitempty"`
	Buckets []bucketJSON       `json:"buckets,omitempty"`
	Sum     *float64           `json:"sum,omitempty"`
	Count   *float64           `json:"count,omitempty"`
}

type bucketJSON struct {
	Le    float64 `json:"le"`
	Count int64   `json:"count"`
}

type metricsResponse struct {
	Metrics []metricJSON `json:"metrics"`
}

// ServeJSON returns all suzuha_* metrics as JSON, read directly from SQLite.
func (h *MetricsHandler) ServeJSON(w http.ResponseWriter, r *http.Request) {
	var result []metricJSON

	// 1. Read all scalar metrics.
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT name, labels, value FROM metrics WHERE name LIKE 'suzuha_%' ORDER BY name, labels`)
	if err != nil {
		h.logger.Error("メトリクスのクエリに失敗", "error", err)
		http.Error(w, `{"error":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Track histogram sum/count for assembly.
	histSums := make(map[string]float64)
	histCounts := make(map[string]float64)

	for rows.Next() {
		var name, labelsStr string
		var value float64
		if err := rows.Scan(&name, &labelsStr, &value); err != nil {
			continue
		}

		// Histogram sum/count are stored as separate entries.
		if strings.HasSuffix(name, "_sum") {
			baseName := strings.TrimSuffix(name, "_sum")
			histSums[baseName] = value
			continue
		}
		if strings.HasSuffix(name, "_count") {
			baseName := strings.TrimSuffix(name, "_count")
			histCounts[baseName] = value
			continue
		}

		jm := metricJSON{
			Name:  name,
			Help:  metricHelp[name],
			Type:  metricType[name],
			Value: value,
		}

		// Parse labels if not empty.
		if labelsStr != "{}" {
			var labels map[string]string
			if err := json.Unmarshal([]byte(labelsStr), &labels); err == nil && len(labels) > 0 {
				jm.Labels = labels
			}
		}

		result = append(result, jm)
	}

	// 2. Build histogram entries with buckets.
	histBuckets := make(map[string][]bucketJSON)
	bucketRows, err := h.db.QueryContext(r.Context(),
		`SELECT name, le, count FROM metric_histogram_buckets WHERE name LIKE 'suzuha_%' ORDER BY name, le`)
	if err == nil {
		defer bucketRows.Close()
		for bucketRows.Next() {
			var name string
			var le float64
			var count int64
			if err := bucketRows.Scan(&name, &le, &count); err != nil {
				continue
			}
			histBuckets[name] = append(histBuckets[name], bucketJSON{Le: le, Count: count})
		}
	}

	// 3. Add histogram summary entries.
	for name, buckets := range histBuckets {
		sum := histSums[name]
		count := histCounts[name]
		jm := metricJSON{
			Name:    name,
			Help:    metricHelp[name],
			Type:    metricType[name],
			Buckets: buckets,
			Sum:     &sum,
			Count:   &count,
		}
		result = append(result, jm)
	}

	if result == nil {
		result = []metricJSON{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metricsResponse{Metrics: result})
}
