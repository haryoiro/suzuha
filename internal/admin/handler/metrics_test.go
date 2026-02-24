package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"log/slog"
)

func setupMetricsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE metrics (
			name TEXT NOT NULL,
			labels TEXT NOT NULL DEFAULT '{}',
			value REAL NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (name, labels)
		);
		CREATE TABLE metric_histogram_buckets (
			name TEXT NOT NULL,
			le REAL NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (name, le)
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}

func TestServeJSON_Empty(t *testing.T) {
	db := setupMetricsTestDB(t)
	h := NewMetricsHandler(db, slog.Default())

	req := httptest.NewRequest("GET", "/api/metrics/json", nil)
	w := httptest.NewRecorder()
	h.ServeJSON(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}

	var resp metricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(resp.Metrics))
	}
}

func TestServeJSON_Counters(t *testing.T) {
	db := setupMetricsTestDB(t)

	// Insert test counters.
	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('suzuha_llm_tokens_input_total', '{}', 1500)")
	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('suzuha_llm_tokens_output_total', '{}', 750)")

	h := NewMetricsHandler(db, slog.Default())
	req := httptest.NewRequest("GET", "/api/metrics/json", nil)
	w := httptest.NewRecorder()
	h.ServeJSON(w, req)

	var resp metricsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	found := make(map[string]float64)
	for _, m := range resp.Metrics {
		if m.Labels == nil {
			found[m.Name] = m.Value
		}
	}

	if v, ok := found["suzuha_llm_tokens_input_total"]; !ok || v != 1500 {
		t.Errorf("tokens_in: got %v", v)
	}
	if v, ok := found["suzuha_llm_tokens_output_total"]; !ok || v != 750 {
		t.Errorf("tokens_out: got %v", v)
	}
}

func TestServeJSON_CounterVec(t *testing.T) {
	db := setupMetricsTestDB(t)

	db.Exec(`INSERT INTO metrics (name, labels, value) VALUES ('suzuha_tool_calls_total', '{"status":"success","tool":"fetch"}', 10)`)
	db.Exec(`INSERT INTO metrics (name, labels, value) VALUES ('suzuha_tool_calls_total', '{"status":"error","tool":"fetch"}', 2)`)

	h := NewMetricsHandler(db, slog.Default())
	req := httptest.NewRequest("GET", "/api/metrics/json", nil)
	w := httptest.NewRecorder()
	h.ServeJSON(w, req)

	var resp metricsResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	toolMetrics := 0
	for _, m := range resp.Metrics {
		if m.Name == "suzuha_tool_calls_total" {
			toolMetrics++
			if m.Labels["tool"] == "fetch" && m.Labels["status"] == "success" && m.Value != 10 {
				t.Errorf("fetch/success: got %f", m.Value)
			}
			if m.Labels["tool"] == "fetch" && m.Labels["status"] == "error" && m.Value != 2 {
				t.Errorf("fetch/error: got %f", m.Value)
			}
		}
	}
	if toolMetrics != 2 {
		t.Errorf("expected 2 tool metrics, got %d", toolMetrics)
	}
}

func TestServeJSON_Histogram(t *testing.T) {
	db := setupMetricsTestDB(t)

	// Insert histogram data.
	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('suzuha_llm_latency_seconds_sum', '{}', 12.5)")
	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('suzuha_llm_latency_seconds_count', '{}', 5)")
	db.Exec("INSERT INTO metric_histogram_buckets (name, le, count) VALUES ('suzuha_llm_latency_seconds', 0.1, 2)")
	db.Exec("INSERT INTO metric_histogram_buckets (name, le, count) VALUES ('suzuha_llm_latency_seconds', 1.0, 4)")
	db.Exec("INSERT INTO metric_histogram_buckets (name, le, count) VALUES ('suzuha_llm_latency_seconds', 10.0, 5)")

	h := NewMetricsHandler(db, slog.Default())
	req := httptest.NewRequest("GET", "/api/metrics/json", nil)
	w := httptest.NewRecorder()
	h.ServeJSON(w, req)

	var resp metricsResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	var histMetric *metricJSON
	for i, m := range resp.Metrics {
		if m.Name == "suzuha_llm_latency_seconds" && m.Buckets != nil {
			histMetric = &resp.Metrics[i]
			break
		}
	}

	if histMetric == nil {
		t.Fatal("histogram metric not found")
	}

	if histMetric.Sum == nil || *histMetric.Sum != 12.5 {
		t.Errorf("sum: got %v", histMetric.Sum)
	}
	if histMetric.Count == nil || *histMetric.Count != 5 {
		t.Errorf("count: got %v", histMetric.Count)
	}
	if len(histMetric.Buckets) != 3 {
		t.Errorf("expected 3 buckets, got %d", len(histMetric.Buckets))
	}
	if histMetric.Buckets[0].Le != 0.1 || histMetric.Buckets[0].Count != 2 {
		t.Errorf("bucket[0]: got le=%f count=%d", histMetric.Buckets[0].Le, histMetric.Buckets[0].Count)
	}
}

func TestServeJSON_NonSuzuhaIgnored(t *testing.T) {
	db := setupMetricsTestDB(t)

	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('other_metric', '{}', 100)")
	db.Exec("INSERT INTO metrics (name, labels, value) VALUES ('suzuha_memory_writes_total', '{}', 42)")

	h := NewMetricsHandler(db, slog.Default())
	req := httptest.NewRequest("GET", "/api/metrics/json", nil)
	w := httptest.NewRecorder()
	h.ServeJSON(w, req)

	var resp metricsResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	for _, m := range resp.Metrics {
		if m.Name == "other_metric" {
			t.Error("non-suzuha metric should not be returned")
		}
	}

	if len(resp.Metrics) != 1 {
		t.Errorf("expected 1 metric, got %d", len(resp.Metrics))
	}
}
