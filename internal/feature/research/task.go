package research

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/external/search"
	"github.com/haryoiro/suzuha/internal/jtime"
	"github.com/haryoiro/suzuha/internal/scheduler"
)

type taskConfig struct {
	SearXNGURL string `json:"searxng_url"`
	MaxSources int    `json:"max_sources"`
}

type taskState struct {
	LastResearchedAt time.Time `json:"last_researched_at"`
}

// Task implements scheduler.CronTask for autonomous web research.
type Task struct {
	mu               sync.Mutex
	lastResearchedAt time.Time
	nowFunc          func() time.Time
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
	maxSources := tc.MaxSources
	if maxSources <= 0 {
		maxSources = defaultMaxSources
	}

	searx := search.NewSearXNG(tc.SearXNGURL)

	article, err := search.RandomArticle(ctx)
	if err != nil {
		cc.Logger.Error("research: wikipedia random", "error", err)
		return nil
	}
	query := article.Title
	cc.Logger.Info("research: starting", "query", query)

	results, err := searx.Search(ctx, query, searchResults)
	if err != nil || len(results) == 0 {
		cc.Logger.Warn("research: no search results")
		return nil
	}

	sources := fetchAll(ctx, searx, results, maxSources, pageMaxRunes)
	if len(sources) == 0 {
		cc.Logger.Warn("research: no pages fetched")
		return nil
	}

	cc.Logger.Info("research: finished", "query", query, "sources", len(sources))

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
	s := taskState{LastResearchedAt: t.lastResearchedAt}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("research: save state", "error", err)
	}
}
