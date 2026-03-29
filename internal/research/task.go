package research

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/haryoiro/suzuha/internal/websearch"
)

// taskConfig holds task-specific configuration from config.yaml.
type taskConfig struct {
	SearXNGURL string `json:"searxng_url"`
	Breadth    int    `json:"breadth"`
	MaxSources int    `json:"max_sources"`
}

// taskState is saved to the task_state table between runs.
type taskState struct {
	LastResearchedAt time.Time `json:"last_researched_at"`
}

// Task implements scheduler.CronTask for fast deep-research exploration.
type Task struct {
	mu               sync.Mutex
	lastResearchedAt time.Time
	nowFunc          func() time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "research" }
func (t *Task) Description() string { return "トピックについて多角的にリサーチする" }

func (t *Task) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return jtime.Now()
}

func (t *Task) Setup(ctx context.Context, cc *scheduler.CronContext) error {
	if cc.DB == nil {
		return nil
	}
	var s taskState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("research: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.lastResearchedAt = s.LastResearchedAt
	t.mu.Unlock()
	return nil
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var tc taskConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &tc)
	}
	if tc.SearXNGURL == "" {
		cc.Logger.Warn("research: no searxng_url configured, skipping")
		return nil
	}
	breadth := tc.Breadth
	if breadth <= 0 {
		breadth = defaultBreadth
	}
	maxSources := tc.MaxSources
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}

	searx := websearch.NewSearXNG(tc.SearXNGURL)

	// Pick a random starting topic.
	article, err := websearch.RandomArticle(ctx)
	if err != nil {
		cc.Logger.Error("research: wikipedia random", "error", err)
		return nil
	}
	query := article.Title
	cc.Logger.Info("research: starting research", "query", query)

	// Step 1: Expand query.
	queries, err := expandQuery(ctx, cc.LLM, cc.SystemPrompt, query, breadth)
	if err != nil {
		queries = []string{query}
	}
	cc.Logger.Info("research: expanded queries", "queries", queries)

	// Step 2: Parallel search.
	results := searchAll(ctx, searx, queries, searchPerQuery)
	if len(results) == 0 {
		cc.Logger.Warn("research: no search results")
		return nil
	}
	cc.Logger.Info("research: search complete", "results", len(results))

	// Step 3: Parallel fetch.
	sources := fetchAll(ctx, searx, results, maxSources, pageMaxRunes)
	if len(sources) == 0 {
		cc.Logger.Warn("research: no pages fetched")
		return nil
	}
	cc.Logger.Info("research: pages fetched", "sources", len(sources))

	// Log completion. Results are not saved to memory — the research cron
	// task collects sources but doesn't have agent context to interpret them.
	// If needed, a future version could add LLM synthesis here.
	cc.Logger.Info("research: finished",
		"query", query,
		"sources", len(sources))

	// Persist state.
	t.mu.Lock()
	t.lastResearchedAt = t.now()
	t.mu.Unlock()
	t.saveState(ctx, cc)

	return nil
}

func (t *Task) saveState(ctx context.Context, cc *scheduler.CronContext) {
	if cc.DB == nil {
		return
	}
	t.mu.Lock()
	s := taskState{
		LastResearchedAt: t.lastResearchedAt,
	}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("research: save state", "error", err)
	}
}
