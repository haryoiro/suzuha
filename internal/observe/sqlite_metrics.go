package observe

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// defBuckets mirrors prometheus.DefBuckets for histogram bucket boundaries.
var defBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// SQLCounter is a cumulative counter backed by SQLite.
type SQLCounter struct {
	db     *sql.DB
	name   string
	labels string // JSON-encoded labels, "{}" for no labels
}

// NewSQLCounter creates a counter with the given name and no labels.
func NewSQLCounter(db *sql.DB, name string) *SQLCounter {
	return &SQLCounter{db: db, name: name, labels: "{}"}
}

func (c *SQLCounter) Inc() { c.Add(1) }

func (c *SQLCounter) Add(v float64) {
	_, _ = c.db.Exec(
		`INSERT INTO metrics (name, labels, value, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name, labels) DO UPDATE
		 SET value = value + ?, updated_at = CURRENT_TIMESTAMP`,
		c.name, c.labels, v, v,
	)
}

// SQLGauge is a point-in-time value backed by SQLite.
type SQLGauge struct {
	db     *sql.DB
	name   string
	labels string
}

// NewSQLGauge creates a gauge with the given name.
func NewSQLGauge(db *sql.DB, name string) *SQLGauge {
	return &SQLGauge{db: db, name: name, labels: "{}"}
}

func (g *SQLGauge) Set(v float64) {
	_, _ = g.db.Exec(
		`INSERT INTO metrics (name, labels, value, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name, labels) DO UPDATE
		 SET value = ?, updated_at = CURRENT_TIMESTAMP`,
		g.name, g.labels, v, v,
	)
}

// SQLHistogram records observations into predefined buckets, with sum and count.
type SQLHistogram struct {
	db      *sql.DB
	name    string
	buckets []float64
}

// NewSQLHistogram creates a histogram and initializes bucket rows.
func NewSQLHistogram(db *sql.DB, name string) *SQLHistogram {
	h := &SQLHistogram{db: db, name: name, buckets: defBuckets}

	// Initialize bucket rows.
	for _, le := range h.buckets {
		_, _ = db.Exec(
			`INSERT OR IGNORE INTO metric_histogram_buckets (name, le, count) VALUES (?, ?, 0)`,
			name, le,
		)
	}

	// Initialize sum and count rows.
	for _, suffix := range []string{"_sum", "_count"} {
		_, _ = db.Exec(
			`INSERT OR IGNORE INTO metrics (name, labels, value, updated_at)
			 VALUES (?, '{}', 0, CURRENT_TIMESTAMP)`,
			name+suffix,
		)
	}

	return h
}

func (h *SQLHistogram) Observe(v float64) {
	// Update sum.
	_, _ = h.db.Exec(
		`UPDATE metrics SET value = value + ?, updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND labels = '{}'`,
		v, h.name+"_sum",
	)

	// Update count.
	_, _ = h.db.Exec(
		`UPDATE metrics SET value = value + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE name = ? AND labels = '{}'`,
		h.name+"_count",
	)

	// Update all buckets where le >= v.
	_, _ = h.db.Exec(
		`UPDATE metric_histogram_buckets SET count = count + 1
		 WHERE name = ? AND le >= ?`,
		h.name, v,
	)
}

// SQLCounterVec is a set of counters grouped by label values.
type SQLCounterVec struct {
	db        *sql.DB
	name      string
	labelKeys []string
}

// NewSQLCounterVec creates a counter vector with the given label dimensions.
func NewSQLCounterVec(db *sql.DB, name string, labelKeys []string) *SQLCounterVec {
	return &SQLCounterVec{db: db, name: name, labelKeys: labelKeys}
}

// WithLabelValues returns a counter for the given label values.
func (cv *SQLCounterVec) WithLabelValues(lvs ...string) *SQLCounter {
	labels := make(map[string]string, len(cv.labelKeys))
	for i, k := range cv.labelKeys {
		if i < len(lvs) {
			labels[k] = lvs[i]
		}
	}

	// Deterministic JSON encoding (sorted keys).
	labelsJSON := labelsToJSON(labels)

	return &SQLCounter{db: cv.db, name: cv.name, labels: labelsJSON}
}

// labelsToJSON encodes labels as deterministic JSON with sorted keys.
func labelsToJSON(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kJSON, _ := json.Marshal(k)
		vJSON, _ := json.Marshal(labels[k])
		fmt.Fprintf(&sb, "%s:%s", kJSON, vJSON)
	}
	sb.WriteByte('}')
	return sb.String()
}
