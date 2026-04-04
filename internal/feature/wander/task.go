package wander

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/external/search"
	"github.com/haryoiro/suzuha/internal/lib/jtime"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
)

const (
	defaultMaxDepth  = 4
	contentMaxRunes  = 800
	searchResultsMax = 8
	snippetMaxRunes  = 120
)

// wanderConfig holds task-specific configuration from config.yaml.
type wanderConfig struct {
	SearXNGURL string `json:"searxng_url"`
	MaxDepth   int    `json:"max_depth"`
}

// persistedState is saved to the task_state table between runs.
type persistedState struct {
	UnexploredInterests []string  `json:"unexplored_interests"`
	LastWanderedAt      time.Time `json:"last_wandered_at"`
}

// hop records one step in the exploration path.
type hop struct {
	Title      string
	Impression string
}

// evaluation is the structured response from the LLM.
type evaluation struct {
	Impression string  `json:"impression"`
	Remember   bool    `json:"remember"`
	NextQuery  *string `json:"next_query"`
	Pick       int     `json:"pick"` // 1-based index into search results, 0 = none
}

// Task implements scheduler.CronTask for autonomous web wandering.
type Task struct {
	mu             sync.Mutex
	unexplored     []string
	lastWanderedAt time.Time
	nowFunc        func() time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "wander" }
func (t *Task) Description() string { return "自律的にネットを散歩する" }

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
	var s persistedState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("wander: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.unexplored = s.UnexploredInterests
	t.lastWanderedAt = s.LastWanderedAt
	t.mu.Unlock()
	cc.Logger.Info("wander: restored state",
		"unexplored", len(s.UnexploredInterests),
		"last_wandered_at", s.LastWanderedAt)
	return nil
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var wc wanderConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &wc)
	}
	if wc.SearXNGURL == "" {
		cc.Logger.Warn("wander: no searxng_url configured, skipping")
		return nil
	}
	maxDepth := wc.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}

	searx := search.NewSearXNG(wc.SearXNGURL)
	systemPrompt := cc.SystemPrompt

	// --- Step 1: Determine starting point ---
	var startTitle, startContent string

	t.mu.Lock()
	unexplored := append([]string(nil), t.unexplored...)
	t.mu.Unlock()

	if len(unexplored) > 0 && rand.Float64() < 0.7 {
		idx := rand.IntN(len(unexplored))
		query := unexplored[idx]
		unexplored = append(unexplored[:idx], unexplored[idx+1:]...)
		t.mu.Lock()
		t.unexplored = unexplored
		t.mu.Unlock()

		cc.Logger.Info("wander: starting from unexplored interest", "query", query)
		results, err := searx.Search(ctx, query, searchResultsMax)
		if err != nil || len(results) == 0 {
			cc.Logger.Warn("wander: search failed, falling back to wikipedia", "error", err)
		} else {
			startTitle = results[0].Title
			startContent = results[0].Content
		}
	}

	if startTitle == "" {
		article, err := search.RandomArticle(ctx)
		if err != nil {
			cc.Logger.Error("wander: wikipedia random", "error", err)
			return nil
		}
		startTitle = article.Title
		startContent = article.Extract
		cc.Logger.Info("wander: starting from wikipedia", "title", article.Title)
	}

	// --- Step 2: Exploration loop ---
	// Each hop: search first, then one LLM call (evaluate + pick combined).
	path := []hop{}
	title := startTitle
	content := startContent
	var rememberedItems []string

	for depth := 0; depth < maxDepth; depth++ {
		// Pre-search related topics so LLM can pick in the same call.
		var searchResults []search.SearchResult
		if depth < maxDepth-1 {
			// Broad search based on current title to give LLM options.
			searchResults, _ = searx.Search(ctx, title, searchResultsMax)
		}

		// Single LLM call: evaluate content + pick next result.
		eval, err := evaluateAndPick(ctx, cc.LLM, systemPrompt, title, content, path, searchResults)
		if err != nil {
			cc.Logger.Warn("wander: evaluate失敗、リトライ中", "error", err, "depth", depth)
			select {
			case <-ctx.Done():
				cc.Logger.Warn("wander: コンテキストがキャンセルされました")
				return nil
			case <-time.After(3 * time.Second):
			}
			eval, err = evaluateAndPick(ctx, cc.LLM, systemPrompt, title, content, path, searchResults)
			if err != nil {
				cc.Logger.Error("wander: evaluate リトライも失敗", "error", err, "depth", depth)
				break
			}
		}

		path = append(path, hop{Title: title, Impression: eval.Impression})
		cc.Logger.Info("wander: hop",
			"depth", depth,
			"title", title,
			"impression", eval.Impression,
			"remember", eval.Remember,
			"next_query", eval.NextQuery,
			"pick", eval.Pick)

		if eval.Remember {
			item := fmt.Sprintf("%s — %s", title, eval.Impression)
			rememberedItems = append(rememberedItems, item)
		}

		if eval.NextQuery == nil || *eval.NextQuery == "" {
			cc.Logger.Info("wander: satisfied, stopping", "depth", depth)
			break
		}

		if depth == maxDepth-1 {
			t.mu.Lock()
			t.unexplored = append(t.unexplored, *eval.NextQuery)
			if len(t.unexplored) > 20 {
				t.unexplored = t.unexplored[len(t.unexplored)-20:]
			}
			t.mu.Unlock()
			cc.Logger.Info("wander: depth limit, saving unexplored interest",
				"next_query", *eval.NextQuery)
			break
		}

		// Use LLM's pick if valid, otherwise search for next_query.
		var picked *search.SearchResult
		if eval.Pick > 0 && eval.Pick <= len(searchResults) {
			picked = &searchResults[eval.Pick-1]
		} else {
			// LLM didn't pick from pre-search; search with next_query.
			results, sErr := searx.Search(ctx, *eval.NextQuery, searchResultsMax)
			if sErr != nil || len(results) == 0 {
				cc.Logger.Warn("wander: search for next topic failed",
					"query", *eval.NextQuery, "error", sErr)
				break
			}
			picked = &results[0]
		}

		pageContent, err := searx.FetchPage(ctx, picked.URL, contentMaxRunes)
		if err != nil {
			cc.Logger.Warn("wander: fetch page failed", "url", picked.URL, "error", err)
			pageContent = picked.Content
		}

		title = picked.Title
		content = pageContent
	}

	// --- Step 3: Reflect and save ---
	if len(path) == 0 {
		cc.Logger.Warn("wander: 探索できずに終了（hopなし）", "start_title", startTitle)
	}
	if len(path) > 0 {
		summary, err := reflectOnExploration(ctx, cc.LLM, systemPrompt, path, rememberedItems)
		if err != nil || summary == "" {
			cc.Logger.Warn("wander: reflection failed, using fallback summary", "error", err)
			summary = buildSummary(path, rememberedItems)
		}

		mem := &memory.Memory{
			Type:    memory.MemoryTypeWorld,
			Content: summary,
			Metadata: map[string]any{
				"source": "wander",
				"type":   "reflection",
			},
		}
		if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
			cc.Logger.Error("wander: save summary", "error", saveErr)
		}
		cc.Logger.Info("wander: finished", "hops", len(path),
			"remembered", len(rememberedItems),
			"reflection_length", len(summary))
	}

	// --- Step 4: Persist state ---
	t.mu.Lock()
	t.lastWanderedAt = t.now()
	t.mu.Unlock()
	t.saveState(ctx, cc)

	return nil
}

func (t *Task) saveState(ctx context.Context, cc *scheduler.CronContext) {
	if cc.DB == nil {
		return
	}
	t.mu.Lock()
	s := persistedState{
		UnexploredInterests: t.unexplored,
		LastWanderedAt:      t.lastWanderedAt,
	}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("wander: save state", "error", err)
	}
}

