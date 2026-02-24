package observe

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create metrics tables.
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

func getMetricValue(t *testing.T, db *sql.DB, name, labels string) float64 {
	t.Helper()
	var value float64
	err := db.QueryRow("SELECT value FROM metrics WHERE name = ? AND labels = ?", name, labels).Scan(&value)
	if err != nil {
		t.Fatalf("get metric %s: %v", name, err)
	}
	return value
}

func TestSQLCounter_Inc(t *testing.T) {
	db := setupTestDB(t)
	c := NewSQLCounter(db, "test_counter")

	c.Inc()
	c.Inc()
	c.Inc()

	v := getMetricValue(t, db, "test_counter", "{}")
	if v != 3 {
		t.Errorf("expected 3, got %f", v)
	}
}

func TestSQLCounter_Add(t *testing.T) {
	db := setupTestDB(t)
	c := NewSQLCounter(db, "test_counter")

	c.Add(10.5)
	c.Add(5.5)

	v := getMetricValue(t, db, "test_counter", "{}")
	if v != 16 {
		t.Errorf("expected 16, got %f", v)
	}
}

func TestSQLGauge_Set(t *testing.T) {
	db := setupTestDB(t)
	g := NewSQLGauge(db, "test_gauge")

	g.Set(0.75)
	v := getMetricValue(t, db, "test_gauge", "{}")
	if v != 0.75 {
		t.Errorf("expected 0.75, got %f", v)
	}

	// Set overwrites.
	g.Set(0.5)
	v2 := getMetricValue(t, db, "test_gauge", "{}")
	if v2 != 0.5 {
		t.Errorf("expected 0.5, got %f", v2)
	}
}

func TestSQLHistogram_Observe(t *testing.T) {
	db := setupTestDB(t)
	h := NewSQLHistogram(db, "test_hist")

	h.Observe(0.003) // falls in 0.005 bucket
	h.Observe(0.1)   // falls in 0.1 bucket
	h.Observe(0.5)   // falls in 0.5 bucket
	h.Observe(7.0)   // falls in 10 bucket

	// Check sum and count.
	sum := getMetricValue(t, db, "test_hist_sum", "{}")
	if sum != 7.603 {
		t.Errorf("sum: expected 7.603, got %f", sum)
	}

	count := getMetricValue(t, db, "test_hist_count", "{}")
	if count != 4 {
		t.Errorf("count: expected 4, got %f", count)
	}

	// Check bucket counts.
	type bucket struct {
		le    float64
		count int
	}
	rows, err := db.Query("SELECT le, count FROM metric_histogram_buckets WHERE name = ? ORDER BY le", "test_hist")
	if err != nil {
		t.Fatalf("query buckets: %v", err)
	}
	defer rows.Close()

	var buckets []bucket
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.le, &b.count); err != nil {
			t.Fatal(err)
		}
		buckets = append(buckets, b)
	}

	if len(buckets) != len(defBuckets) {
		t.Fatalf("expected %d buckets, got %d", len(defBuckets), len(buckets))
	}

	// 0.003 → le=0.005: count=1
	if buckets[0].count != 1 {
		t.Errorf("bucket le=0.005: expected 1, got %d", buckets[0].count)
	}

	// All 4 observations fall in le=10 bucket.
	lastBucket := buckets[len(buckets)-1]
	if lastBucket.count != 4 {
		t.Errorf("bucket le=10: expected 4, got %d", lastBucket.count)
	}
}

func TestSQLCounterVec(t *testing.T) {
	db := setupTestDB(t)
	cv := NewSQLCounterVec(db, "test_vec", []string{"tool", "status"})

	cv.WithLabelValues("fetch", "success").Inc()
	cv.WithLabelValues("fetch", "success").Inc()
	cv.WithLabelValues("fetch", "error").Inc()
	cv.WithLabelValues("search", "success").Add(5)

	// Check individual counters.
	v1 := getMetricValue(t, db, "test_vec", `{"status":"success","tool":"fetch"}`)
	if v1 != 2 {
		t.Errorf("fetch/success: expected 2, got %f", v1)
	}

	v2 := getMetricValue(t, db, "test_vec", `{"status":"error","tool":"fetch"}`)
	if v2 != 1 {
		t.Errorf("fetch/error: expected 1, got %f", v2)
	}

	v3 := getMetricValue(t, db, "test_vec", `{"status":"success","tool":"search"}`)
	if v3 != 5 {
		t.Errorf("search/success: expected 5, got %f", v3)
	}
}

func TestNewMetrics(t *testing.T) {
	db := setupTestDB(t)
	m := NewMetrics(db)

	// Verify all fields are non-nil.
	if m.LLMLatency == nil {
		t.Error("LLMLatency is nil")
	}
	if m.LLMTokensIn == nil {
		t.Error("LLMTokensIn is nil")
	}
	if m.LLMTokensOut == nil {
		t.Error("LLMTokensOut is nil")
	}
	if m.EmbeddingLatency == nil {
		t.Error("EmbeddingLatency is nil")
	}
	if m.ContextWindowUsage == nil {
		t.Error("ContextWindowUsage is nil")
	}
	if m.ToolCallsTotal == nil {
		t.Error("ToolCallsTotal is nil")
	}
	if m.EventsTotal == nil {
		t.Error("EventsTotal is nil")
	}
	if m.MemoryWritesTotal == nil {
		t.Error("MemoryWritesTotal is nil")
	}
}

func TestMetrics_Persistence(t *testing.T) {
	db := setupTestDB(t)

	// First "session": create metrics and record data.
	m1 := NewMetrics(db)
	m1.LLMTokensIn.Add(100)
	m1.LLMTokensOut.Add(50)
	m1.ToolCallsTotal.WithLabelValues("fetch", "success").Inc()
	m1.EventsTotal.WithLabelValues("discord", "message").Add(10)
	m1.LLMLatency.Observe(1.5)

	// Second "session": create new metrics from same DB.
	m2 := NewMetrics(db)

	// Add more.
	m2.LLMTokensIn.Add(200)
	m2.ToolCallsTotal.WithLabelValues("fetch", "success").Inc()

	// Verify cumulative values.
	v := getMetricValue(t, db, "suzuha_llm_tokens_input_total", "{}")
	if v != 300 {
		t.Errorf("tokens in: expected 300, got %f", v)
	}

	v2 := getMetricValue(t, db, "suzuha_llm_tokens_output_total", "{}")
	if v2 != 50 {
		t.Errorf("tokens out: expected 50, got %f", v2)
	}

	v3 := getMetricValue(t, db, "suzuha_tool_calls_total", `{"status":"success","tool":"fetch"}`)
	if v3 != 2 {
		t.Errorf("tool calls: expected 2, got %f", v3)
	}

	v4 := getMetricValue(t, db, "suzuha_llm_latency_seconds_sum", "{}")
	if v4 != 1.5 {
		t.Errorf("latency sum: expected 1.5, got %f", v4)
	}
}

func TestLabelsToJSON(t *testing.T) {
	tests := []struct {
		labels map[string]string
		want   string
	}{
		{nil, "{}"},
		{map[string]string{}, "{}"},
		{map[string]string{"a": "1"}, `{"a":"1"}`},
		{map[string]string{"b": "2", "a": "1"}, `{"a":"1","b":"2"}`}, // sorted
	}

	for _, tc := range tests {
		got := labelsToJSON(tc.labels)
		if got != tc.want {
			t.Errorf("labelsToJSON(%v): got %q, want %q", tc.labels, got, tc.want)
		}
	}
}
