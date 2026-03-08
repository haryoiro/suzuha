package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

var metricHelp = map[string]string{
	"suzuha_llm_tokens_input_total":     "Total input tokens sent to LLM",
	"suzuha_llm_tokens_output_total":    "Total output tokens received from LLM",
	"suzuha_llm_latency_seconds":        "LLM request latency in seconds",
	"suzuha_embedding_latency_seconds":  "Embedding API request latency in seconds",
	"suzuha_context_window_usage_ratio": "Current context window usage ratio (0-1)",
	"suzuha_tool_calls_total":           "Total tool calls by tool name and status",
	"suzuha_events_total":               "Total events by source and type",
	"suzuha_memory_writes_total":        "Total writes to long-term memory",
}

var metricType = map[string]api.MetricEntryType{
	"suzuha_llm_tokens_input_total":     api.MetricEntryTypeCounter,
	"suzuha_llm_tokens_output_total":    api.MetricEntryTypeCounter,
	"suzuha_llm_latency_seconds":        api.MetricEntryTypeHistogram,
	"suzuha_embedding_latency_seconds":  api.MetricEntryTypeHistogram,
	"suzuha_context_window_usage_ratio": api.MetricEntryTypeGauge,
	"suzuha_tool_calls_total":           api.MetricEntryTypeCounter,
	"suzuha_events_total":               api.MetricEntryTypeCounter,
	"suzuha_memory_writes_total":        api.MetricEntryTypeCounter,
}

func (h *AdminHandler) MetricsJSON(ctx context.Context) (*api.MetricsJSONOK, error) {
	var result []api.MetricEntry

	rows, err := h.db.QueryContext(ctx,
		`SELECT name, labels, value FROM metrics WHERE name LIKE 'suzuha_%' ORDER BY name, labels`)
	if err != nil {
		h.logger.Error("メトリクスのクエリに失敗", "error", err.Error())
		return nil, fmt.Errorf("query failed")
	}
	defer rows.Close()

	histSums := make(map[string]float64)
	histCounts := make(map[string]float64)

	for rows.Next() {
		var name, labelsStr string
		var value float64
		if err := rows.Scan(&name, &labelsStr, &value); err != nil {
			continue
		}

		if strings.HasSuffix(name, "_sum") {
			histSums[strings.TrimSuffix(name, "_sum")] = value
			continue
		}
		if strings.HasSuffix(name, "_count") {
			histCounts[strings.TrimSuffix(name, "_count")] = value
			continue
		}

		jm := api.MetricEntry{
			Name:  name,
			Help:  metricHelp[name],
			Type:  metricType[name],
			Value: api.NewOptFloat64(value),
		}

		if labelsStr != "{}" {
			var labels map[string]string
			if err := json.Unmarshal([]byte(labelsStr), &labels); err == nil && len(labels) > 0 {
				jm.Labels = api.NewOptMetricEntryLabels(api.MetricEntryLabels(labels))
			}
		}

		result = append(result, jm)
	}

	histBuckets := make(map[string][]api.MetricBucket)
	bucketRows, err := h.db.QueryContext(ctx,
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
			histBuckets[name] = append(histBuckets[name], api.MetricBucket{Le: le, Count: count})
		}
	}

	for name, buckets := range histBuckets {
		sum := histSums[name]
		count := histCounts[name]
		jm := api.MetricEntry{
			Name:    name,
			Help:    metricHelp[name],
			Type:    metricType[name],
			Buckets: buckets,
			Sum:     api.NewOptFloat64(sum),
			Count:   api.NewOptFloat64(count),
		}
		result = append(result, jm)
	}

	if result == nil {
		result = []api.MetricEntry{}
	}
	return &api.MetricsJSONOK{Metrics: result}, nil
}
