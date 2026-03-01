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
	"github.com/mozilla-ai/any-llm-go/providers"
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
		// Evaluate the current content.
		eval, err := t.evaluate(ctx, cc, systemPrompt, title, content, path)
		if err != nil {
			cc.Logger.Error("explore: evaluate", "error", err, "depth", depth)
			break
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

		// Let LLM pick which result to read.
		picked, err := t.pickResult(ctx, cc, systemPrompt, nextQuery, results, path)
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

	// --- Step 3: Save exploration summary ---
	if len(path) > 0 {
		summary := t.buildSummary(path, rememberedItems)
		mem := &memory.Memory{
			Type:    memory.MemoryTypeWorld,
			Content: summary,
			Metadata: map[string]any{
				"source": "explore",
				"type":   "summary",
			},
		}
		if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
			cc.Logger.Error("explore: save summary", "error", saveErr)
		}
		cc.Logger.Info("explore: finished", "hops", len(path),
			"remembered", len(rememberedItems))
	}

	// --- Step 4: Persist state ---
	t.mu.Lock()
	t.lastExploredAt = t.now()
	t.mu.Unlock()
	t.saveState(ctx, cc)

	return nil
}

// evaluate asks the LLM to evaluate content from のの's perspective.
func (t *Task) evaluate(
	ctx context.Context,
	cc *scheduler.CronContext,
	systemPrompt string,
	title, content string,
	path []hop,
) (*evaluation, error) {
	var sb strings.Builder

	sb.WriteString("今読んでるもの:\n")
	fmt.Fprintf(&sb, "タイトル: %s\n", title)
	fmt.Fprintf(&sb, "内容: %s\n\n", truncateRunes(content, contentMaxRunes))

	if len(path) > 0 {
		sb.WriteString("ここまでの探索:\n")
		for i, h := range path {
			fmt.Fprintf(&sb, "%d. 「%s」→ %s\n", i+1, h.Title, h.Impression)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("気になることがあったら何が気になるか教えて。\n")
	sb.WriteString("もう十分なら next_query を null にして。\n\n")
	sb.WriteString("JSON で返して（これだけ出力して）:\n")
	sb.WriteString(`{"impression": "感想1-2文", "remember": true/false, "next_query": "キーワード" or null}`)
	sb.WriteString("\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	// Strip markdown code fences if present.
	text = stripCodeFence(text)

	var eval evaluation
	if err := json.Unmarshal([]byte(text), &eval); err != nil {
		// If JSON parsing fails, treat as "not interested".
		cc.Logger.Warn("explore: failed to parse evaluation, stopping",
			"raw", text, "error", err)
		return &evaluation{
			Impression: text,
			Remember:   false,
			NextQuery:  nil,
		}, nil
	}
	return &eval, nil
}

// pickResult asks the LLM to choose a search result to read.
func (t *Task) pickResult(
	ctx context.Context,
	cc *scheduler.CronContext,
	systemPrompt string,
	query string,
	results []SearchResult,
	path []hop,
) (*SearchResult, error) {
	var sb strings.Builder

	fmt.Fprintf(&sb, "「%s」で検索した結果:\n\n", query)
	sb.WriteString(truncateResults(results, snippetMaxRunes))
	sb.WriteString("\n")

	if len(path) > 0 {
		sb.WriteString("ここまでの探索:\n")
		for i, h := range path {
			fmt.Fprintf(&sb, "%d. 「%s」→ %s\n", i+1, h.Title, h.Impression)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("どれが一番気になる？番号で答えて（どれも気にならなければ 0）。\n")
	sb.WriteString("数字だけ出力して。\n")

	messages := []providers.Message{
		{Role: "user", Content: sb.String()},
	}
	if systemPrompt != "" {
		messages = append([]providers.Message{{Role: "system", Content: systemPrompt}}, messages...)
	}

	resp, err := cc.LLM.CompleteRaw(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm pick: %w", err)
	}

	text := strings.TrimSpace(resp.Text)
	// Extract first digit sequence.
	var idx int
	for _, r := range text {
		if r >= '0' && r <= '9' {
			idx = idx*10 + int(r-'0')
		} else if idx > 0 {
			break
		}
	}

	if idx <= 0 || idx > len(results) {
		return nil, nil // nothing picked
	}
	return &results[idx-1], nil
}

func (t *Task) buildSummary(path []hop, remembered []string) string {
	var sb strings.Builder
	sb.WriteString("ネット散歩: ")

	titles := make([]string, len(path))
	for i, h := range path {
		titles[i] = h.Title
	}
	sb.WriteString(strings.Join(titles, " → "))

	if len(remembered) > 0 {
		sb.WriteString("\n覚えたこと: ")
		sb.WriteString(strings.Join(remembered, " / "))
	}

	return sb.String()
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
