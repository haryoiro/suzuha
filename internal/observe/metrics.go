package observe

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all application metrics.
type Metrics struct {
	LLMLatency         prometheus.Histogram
	LLMTokensIn        prometheus.Counter
	LLMTokensOut       prometheus.Counter
	EmbeddingLatency   prometheus.Histogram
	ContextWindowUsage prometheus.Gauge
	ToolCallsTotal     *prometheus.CounterVec
	EventsTotal        *prometheus.CounterVec
	MemoryWritesTotal  prometheus.Counter
}

// NewMetrics creates and registers all prometheus metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		LLMLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "suzuha_llm_latency_seconds",
			Help:    "LLM request latency in seconds",
			Buckets: prometheus.DefBuckets,
		}),
		LLMTokensIn: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "suzuha_llm_tokens_input_total",
			Help: "Total input tokens sent to LLM",
		}),
		LLMTokensOut: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "suzuha_llm_tokens_output_total",
			Help: "Total output tokens received from LLM",
		}),
		EmbeddingLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "suzuha_embedding_latency_seconds",
			Help:    "Embedding API request latency in seconds",
			Buckets: prometheus.DefBuckets,
		}),
		ContextWindowUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "suzuha_context_window_usage_ratio",
			Help: "Current context window usage ratio (0-1)",
		}),
		ToolCallsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "suzuha_tool_calls_total",
			Help: "Total tool calls by tool name and status",
		}, []string{"tool", "status"}),
		EventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "suzuha_events_total",
			Help: "Total events by source and type",
		}, []string{"source", "type"}),
		MemoryWritesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "suzuha_memory_writes_total",
			Help: "Total writes to long-term memory",
		}),
	}

	reg.MustRegister(
		m.LLMLatency,
		m.LLMTokensIn,
		m.LLMTokensOut,
		m.EmbeddingLatency,
		m.ContextWindowUsage,
		m.ToolCallsTotal,
		m.EventsTotal,
		m.MemoryWritesTotal,
	)

	return m
}

// Handler returns an HTTP handler for the /metrics endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}
