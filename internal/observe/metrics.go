package observe

import "database/sql"

// Metrics holds all application metrics, backed by SQLite for persistence across restarts.
type Metrics struct {
	LLMLatency         *SQLHistogram
	LLMTokensIn        *SQLCounter
	LLMTokensOut       *SQLCounter
	EmbeddingLatency   *SQLHistogram
	ContextWindowUsage *SQLGauge
	ToolCallsTotal     *SQLCounterVec
	EventsTotal        *SQLCounterVec
	MemoryWritesTotal  *SQLCounter
}

// NewMetrics creates and initializes all SQLite-backed metrics.
func NewMetrics(db *sql.DB) *Metrics {
	return &Metrics{
		LLMLatency:         NewSQLHistogram(db, "suzuha_llm_latency_seconds"),
		LLMTokensIn:        NewSQLCounter(db, "suzuha_llm_tokens_input_total"),
		LLMTokensOut:       NewSQLCounter(db, "suzuha_llm_tokens_output_total"),
		EmbeddingLatency:   NewSQLHistogram(db, "suzuha_embedding_latency_seconds"),
		ContextWindowUsage: NewSQLGauge(db, "suzuha_context_window_usage_ratio"),
		ToolCallsTotal:     NewSQLCounterVec(db, "suzuha_tool_calls_total", []string{"tool", "status"}),
		EventsTotal:        NewSQLCounterVec(db, "suzuha_events_total", []string{"source", "type"}),
		MemoryWritesTotal:  NewSQLCounter(db, "suzuha_memory_writes_total"),
	}
}
