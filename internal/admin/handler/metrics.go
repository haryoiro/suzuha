package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
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
	Help   string            `json:"help"`
	Type   string            `json:"type"`
	Value  float64           `json:"value,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// Histogram-specific fields.
	Buckets []bucketJSON `json:"buckets,omitempty"`
	Sum     float64      `json:"sum,omitempty"`
	Count   uint64       `json:"count,omitempty"`
}

type bucketJSON struct {
	Le    float64 `json:"le"`
	Count uint64  `json:"count"`
}

type metricsResponse struct {
	Metrics []metricJSON `json:"metrics"`
}

// ProxyJSON fetches raw Prometheus metrics and returns them as structured JSON.
func (h *MetricsHandler) ProxyJSON(w http.ResponseWriter, r *http.Request) {
	resp, err := h.client.Get(h.agentURL)
	if err != nil {
		h.logger.Error("proxy metrics json", "error", err)
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	parser := expfmt.TextParser{}
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		h.logger.Error("parse metrics", "error", err)
		http.Error(w, `{"error":"parse error"}`, http.StatusInternalServerError)
		return
	}

	var result []metricJSON
	for name, family := range families {
		if !strings.HasPrefix(name, "suzuha_") {
			continue
		}
		for _, m := range family.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}

			jm := metricJSON{
				Name:   name,
				Help:   family.GetHelp(),
				Type:   family.GetType().String(),
				Labels: labels,
			}

			switch family.GetType() {
			case dto.MetricType_COUNTER:
				jm.Value = m.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				jm.Value = m.GetGauge().GetValue()
			case dto.MetricType_HISTOGRAM:
				hist := m.GetHistogram()
				jm.Sum = hist.GetSampleSum()
				jm.Count = hist.GetSampleCount()
				for _, b := range hist.GetBucket() {
					jm.Buckets = append(jm.Buckets, bucketJSON{
						Le:    b.GetUpperBound(),
						Count: b.GetCumulativeCount(),
					})
				}
				if hist.GetSampleCount() > 0 {
					jm.Value = math.Round(hist.GetSampleSum()/float64(hist.GetSampleCount())*1000) / 1000
				}
			}

			result = append(result, jm)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metricsResponse{Metrics: result})
}
