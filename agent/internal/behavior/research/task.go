package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/runtime/scheduler"
)

type taskConfig struct {
	SearXNGURL string `json:"searxng_url"`
	MaxSources int    `json:"max_sources"`
}

type taskState struct {
	LastResearchedAt time.Time `json:"last_researched_at"`
}

// Task は自律的に Web 検索を実行する scheduler.CronTask 実装。
type Task struct {
	db     *sql.DB
	logger *slog.Logger

	mu               sync.Mutex
	lastResearchedAt time.Time
	nowFunc          func() time.Time
}

// NewTask は research Task を生成する。db は scheduler 状態永続化用。
func NewTask(db *sql.DB, logger *slog.Logger) *Task {
	return &Task{db: db, logger: logger}
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "research" }
func (t *Task) Description() string { return "ランダムなトピックをリサーチする" }

func (t *Task) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return jtime.Now()
}

// Setup は scheduler 起動時に呼ばれ、前回実行時刻を復元する。
func (t *Task) Setup(ctx context.Context) error {
	if t.db == nil {
		return nil
	}
	var s taskState
	if err := scheduler.LoadState(ctx, t.db, t.Name(), &s); err != nil {
		t.logger.Warn("research: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.lastResearchedAt = s.LastResearchedAt
	t.mu.Unlock()
	return nil
}

// Execute は 1 回分のリサーチを実行する。
func (t *Task) Execute(ctx context.Context, cfg json.RawMessage) error {
	var tc taskConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &tc); err != nil {
			t.logger.Warn("research: config parse failed, using defaults", "error", err)
		}
	}
	if tc.SearXNGURL == "" {
		t.logger.Warn("research: no searxng_url configured, skipping")
		return nil
	}
	maxSources := tc.MaxSources
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}

	searx := NewSearXNG(tc.SearXNGURL)

	article, err := RandomArticle(ctx)
	if err != nil {
		t.logger.Error("research: wikipedia random", "error", err)
		return nil
	}
	query := article.Title
	t.logger.Info("research: starting", "query", query)

	results, err := searx.Search(ctx, query, searchResults)
	if err != nil || len(results) == 0 {
		t.logger.Warn("research: no search results")
		return nil
	}

	sources := fetchAll(ctx, searx, results, maxSources, pageMaxRunes)
	if len(sources) == 0 {
		t.logger.Warn("research: no pages fetched")
		return nil
	}

	t.logger.Info("research: finished", "query", query, "sources", len(sources))

	t.mu.Lock()
	t.lastResearchedAt = t.now()
	t.mu.Unlock()
	t.saveState(ctx)
	return nil
}

func (t *Task) saveState(ctx context.Context) {
	if t.db == nil {
		return
	}
	t.mu.Lock()
	s := taskState{LastResearchedAt: t.lastResearchedAt}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, t.db, t.Name(), &s); err != nil {
		t.logger.Warn("research: save state", "error", err)
	}
}
