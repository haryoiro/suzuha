package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
)

const (
	defaultMaxDepth  = 4
	contentMaxRunes  = 800
	searchResultsMax = 8
	snippetMaxRunes  = 120
)

// exploreConfig holds task-specific configuration from config.yaml.
type exploreConfig struct {
	SearXNGURL string `json:"searxng_url"`
	MaxDepth   int    `json:"max_depth"`
}

// persistedState is saved to the task_state table between runs.
type persistedState struct {
	UnexploredInterests []string  `json:"unexplored_interests"`
	LastExploredAt      time.Time `json:"last_explored_at"`
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
	NextQuery  *string `json:"next_query"` // null = stop
}

// Task implements scheduler.CronTask for autonomous web exploration.
type Task struct {
	mu             sync.Mutex
	unexplored     []string
	lastExploredAt time.Time
	nowFunc        func() time.Time
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "explore" }
func (t *Task) Description() string { return "自律的にネットを探索する" }

func (t *Task) now() time.Time {
	if t.nowFunc != nil {
		return t.nowFunc()
	}
	return time.Now()
}

func (t *Task) Setup(ctx context.Context, cc *scheduler.CronContext) error {
	if cc.DB == nil {
		return nil
	}
	var s persistedState
	if err := scheduler.LoadState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("explore: load state", "error", err)
		return nil
	}
	t.mu.Lock()
	t.unexplored = s.UnexploredInterests
	t.lastExploredAt = s.LastExploredAt
	t.mu.Unlock()
	cc.Logger.Info("explore: restored state",
		"unexplored", len(s.UnexploredInterests),
		"last_explored_at", s.LastExploredAt)
	return nil
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	var ec exploreConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &ec)
	}
	if ec.SearXNGURL == "" {
		cc.Logger.Warn("explore: no searxng_url configured, skipping")
		return nil
	}
	maxDepth := ec.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}

	searx := NewSearXNG(ec.SearXNGURL)
	systemPrompt := cc.SystemPrompt

	// --- Step 1: Determine starting point ---
	var startTitle, startContent string

	t.mu.Lock()
	unexplored := append([]string(nil), t.unexplored...)
	t.mu.Unlock()

	if len(unexplored) > 0 && rand.Float64() < 0.7 {
		// Use a previously unexplored interest.
		idx := rand.IntN(len(unexplored))
		query := unexplored[idx]
		// Remove from list.
		unexplored = append(unexplored[:idx], unexplored[idx+1:]...)
		t.mu.Lock()
		t.unexplored = unexplored
		t.mu.Unlock()

		cc.Logger.Info("explore: starting from unexplored interest", "query", query)
		results, err := searx.Search(ctx, query, searchResultsMax)
		if err != nil || len(results) == 0 {
			cc.Logger.Warn("explore: search failed, falling back to wikipedia", "error", err)
		} else {
			startTitle = results[0].Title
			startContent = results[0].Content
		}
	}

	if startTitle == "" {
		// Fall back to Wikipedia random article.
		article, err := RandomArticle(ctx)
		if err != nil {
			cc.Logger.Error("explore: wikipedia random", "error", err)
			return nil
		}
		startTitle = article.Title
		startContent = article.Extract
		cc.Logger.Info("explore: starting from wikipedia", "title", article.Title)
	}

	// --- Step 2: Exploration loop ---
	path := []hop{}
	title := startTitle
	content := startContent
	var rememberedItems []string

	for depth := 0; depth < maxDepth; depth++ {
		// Evaluate the current content (retry once on transient errors).
		eval, err := evaluate(ctx, cc.LLM, systemPrompt, title, content, path)
		if err != nil {
			cc.Logger.Warn("explore: evaluate失敗、リトライ中", "error", err, "depth", depth)
			select {
			case <-ctx.Done():
				cc.Logger.Warn("explore: コンテキストがキャンセルされました")
				return nil
			case <-time.After(5 * time.Second):
			}
			eval, err = evaluate(ctx, cc.LLM, systemPrompt, title, content, path)
			if err != nil {
				cc.Logger.Error("explore: evaluate リトライも失敗", "error", err, "depth", depth)
				break
			}
		}

		path = append(path, hop{Title: title, Impression: eval.Impression})
		cc.Logger.Info("explore: hop",
			"depth", depth,
			"title", title,
			"impression", eval.Impression,
			"remember", eval.Remember,
			"next_query", eval.NextQuery)

		// Collect interesting items for the summary (saved at the end, not per-hop).
		if eval.Remember {
			item := fmt.Sprintf("%s — %s", title, eval.Impression)
			rememberedItems = append(rememberedItems, item)
		}

		// Decide next step.
		if eval.NextQuery == nil || *eval.NextQuery == "" {
			cc.Logger.Info("explore: satisfied, stopping", "depth", depth)
			break
		}

		if depth == maxDepth-1 {
			// At depth limit — save as unexplored interest for next time.
			t.mu.Lock()
			t.unexplored = append(t.unexplored, *eval.NextQuery)
			// Cap unexplored list.
			if len(t.unexplored) > 20 {
				t.unexplored = t.unexplored[len(t.unexplored)-20:]
			}
			t.mu.Unlock()
			cc.Logger.Info("explore: depth limit, saving unexplored interest",
				"next_query", *eval.NextQuery)
			break
		}

		// Search for the next topic.
		nextQuery := *eval.NextQuery
		results, err := searx.Search(ctx, nextQuery, searchResultsMax)
		if err != nil || len(results) == 0 {
			cc.Logger.Warn("explore: search for next topic failed",
				"query", nextQuery, "error", err)
			break
		}

		// Let LLM pick which result to read (retry once on transient errors).
		picked, err := pickResult(ctx, cc.LLM, systemPrompt, nextQuery, results, path)
		if err != nil {
			cc.Logger.Warn("explore: pickResult失敗、リトライ中", "query", nextQuery, "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			picked, err = pickResult(ctx, cc.LLM, systemPrompt, nextQuery, results, path)
		}
		if err != nil || picked == nil {
			cc.Logger.Warn("explore: pick result failed or none interesting",
				"query", nextQuery, "error", err)
			break
		}

		// Fetch the page.
		pageContent, err := searx.FetchPage(ctx, picked.URL, contentMaxRunes)
		if err != nil {
			cc.Logger.Warn("explore: fetch page failed", "url", picked.URL, "error", err)
			// Use snippet as fallback.
			pageContent = picked.Content
		}

		title = picked.Title
		content = pageContent
	}

	// --- Step 3: Reflect and save ---
	if len(path) == 0 {
		cc.Logger.Warn("explore: 探索できずに終了（hopなし）", "start_title", startTitle)
	}
	if len(path) > 0 {
		// Ask LLM to synthesize a personal takeaway from the exploration.
		summary, err := reflectOnExploration(ctx, cc.LLM, systemPrompt, path, rememberedItems)
		if err != nil || summary == "" {
			cc.Logger.Warn("explore: reflection failed, using fallback summary", "error", err)
			summary = buildSummary(path, rememberedItems)
		}

		mem := &memory.Memory{
			Type:    memory.MemoryTypeWorld,
			Content: summary,
			Metadata: map[string]any{
				"source": "explore",
				"type":   "reflection",
			},
		}
		if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
			cc.Logger.Error("explore: save summary", "error", saveErr)
		}
		cc.Logger.Info("explore: finished", "hops", len(path),
			"remembered", len(rememberedItems),
			"reflection_length", len(summary))
	}

	// --- Step 4: Persist state ---
	t.mu.Lock()
	t.lastExploredAt = t.now()
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
		LastExploredAt:      t.lastExploredAt,
	}
	t.mu.Unlock()
	if err := scheduler.SaveState(ctx, cc.DB, t.Name(), &s); err != nil {
		cc.Logger.Warn("explore: save state", "error", err)
	}
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
