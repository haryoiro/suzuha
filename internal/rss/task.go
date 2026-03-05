package rss

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/scheduler"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// Task implements scheduler.CronTask for RSS feed monitoring and notification.
type Task struct {
	httpClient *http.Client
}

var _ scheduler.CronTask = (*Task)(nil)

func (t *Task) Name() string        { return "rss" }
func (t *Task) Description() string { return "RSS フィード監視・通知" }

// Setup is a no-op because table creation is handled by Feature.Setup.
func (t *Task) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

// rssConfig holds task-specific configuration from config.yaml.
type rssConfig struct {
	VectorThreshold      float64 `json:"vector_threshold"`
	NotifyThreshold      float64 `json:"notify_threshold"`
	MaxArticlesPerNotify int     `json:"max_articles_per_notify"`
}

func defaultRSSConfig() rssConfig {
	return rssConfig{
		VectorThreshold:      0.3,
		NotifyThreshold:      0.6,
		MaxArticlesPerNotify: 5,
	}
}

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
	rc := defaultRSSConfig()
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &rc); err != nil {
			cc.Logger.Warn("rss: 設定のパースに失敗", "error", err)
		}
	}

	store := NewFeedStore(cc.DB)

	// 1. Fetch new articles from all enabled feeds.
	feeds, err := store.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("rss: フィード一覧の取得に失敗: %w", err)
	}
	if len(feeds) == 0 {
		cc.Logger.Debug("rss: 有効なフィードがありません")
		return nil
	}

	var newItemsByChannel = make(map[string][]itemWithFeed)

	for _, feed := range feeds {
		items, fetchErr := fetchFeed(ctx, t.httpClient, feed, store, cc)
		if fetchErr != nil {
			cc.Logger.Error("rss: フィードの取得に失敗", "feed", feed.Name, "url", feed.URL, "error", fetchErr)
			continue
		}
		if len(items) > 0 {
			newItemsByChannel[feed.ChannelID] = append(newItemsByChannel[feed.ChannelID], items...)
		}
		if err := store.UpdateLastPolled(ctx, feed.ID); err != nil {
			cc.Logger.Warn("rss: 最終取得時刻の更新に失敗", "feed", feed.ID, "error", err)
		}
	}

	if len(newItemsByChannel) == 0 {
		cc.Logger.Debug("rss: 新着記事はありません")
		return nil
	}

	// 2. Score and notify per channel.
	for channelID, items := range newItemsByChannel {
		if err := scoreAndNotify(ctx, cc, rc, channelID, items); err != nil {
			cc.Logger.Error("rss: スコアリング/通知に失敗", "channel", channelID, "error", err)
		}
	}

	return nil
}

// itemWithFeed bundles an Item with its parent Feed for context.
type itemWithFeed struct {
	Item Item
	Feed Feed
}

// fetchFeed fetches a single RSS/Atom feed and saves new items.
func fetchFeed(ctx context.Context, client *http.Client, feed Feed, store *FeedStore, cc *scheduler.CronContext) ([]itemWithFeed, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", feed.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエストの作成に失敗: %w", err)
	}
	req.Header.Set("User-Agent", "suzuha-rss/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPステータス %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB limit
	if err != nil {
		return nil, fmt.Errorf("ボディの読み取りに失敗: %w", err)
	}

	entries, err := parseRSSOrAtom(body)
	if err != nil {
		return nil, fmt.Errorf("パースに失敗: %w", err)
	}

	var newItems []itemWithFeed
	for _, entry := range entries {
		guid := entry.GUID
		if guid == "" {
			guid = entry.Link
		}
		if guid == "" {
			continue
		}

		exists, existsErr := store.ItemExists(ctx, feed.ID, guid)
		if existsErr != nil {
			cc.Logger.Warn("rss: アイテムの存在確認に失敗", "feed", feed.ID, "guid", guid, "error", existsErr)
			continue
		}
		if exists {
			continue
		}

		item := &Item{
			FeedID:      feed.ID,
			GUID:        guid,
			Title:       entry.Title,
			Link:        entry.Link,
			Description: entry.Description,
			PublishedAt: entry.Published,
		}
		if err := store.InsertItem(ctx, item); err != nil {
			cc.Logger.Error("rss: アイテムの挿入に失敗", "guid", guid, "error", err)
			continue
		}

		// Save to memory store for vector search.
		content := item.Title
		if item.Description != "" {
			content += "\n" + truncate(item.Description, 500)
		}
		mem := &memory.Memory{
			Type:    memory.MemoryTypeRSS,
			Content: content,
			Metadata: map[string]any{
				"url":     item.Link,
				"feed":    feed.Name,
				"feed_id": feed.ID,
			},
		}
		if saveErr := cc.Memory.Save(ctx, mem); saveErr != nil {
			cc.Logger.Error("rss: メモリの保存に失敗", "item", item.Title, "error", saveErr)
		} else {
			if err := store.UpdateItemMemoryID(ctx, item.ID, mem.ID); err != nil {
				cc.Logger.Warn("rss: アイテムのメモリID更新に失敗", "item", item.ID, "error", err)
			}
		}

		newItems = append(newItems, itemWithFeed{Item: *item, Feed: feed})
	}

	cc.Logger.Info("rss: フィードを取得しました", "feed", feed.Name, "new", len(newItems), "total_entries", len(entries))
	return newItems, nil
}

// scoreAndNotify scores new articles against user interests and sends notifications.
func scoreAndNotify(ctx context.Context, cc *scheduler.CronContext, rc rssConfig, channelID string, items []itemWithFeed) error {
	if len(items) == 0 {
		return nil
	}

	// Collect user interests from memory.
	interests, err := queryUserInterests(ctx, cc)
	if err != nil {
		cc.Logger.Warn("rss: 興味の取得に失敗、全件通知にフォールバック", "error", err)
		return notifyChannel(ctx, cc, channelID, items, rc)
	}

	if len(interests) == 0 {
		// No user interests found, notify with all new articles.
		return notifyChannel(ctx, cc, channelID, items, rc)
	}

	// Phase A: Vector similarity pre-filter.
	candidates := vectorPreFilter(ctx, cc, items, interests, rc.VectorThreshold)
	if len(candidates) == 0 {
		cc.Logger.Debug("rss: ベクトルフィルタ後の候補がありません", "channel", channelID)
		return nil
	}

	// Phase B: LLM batch scoring.
	scored, err := llmBatchScore(ctx, cc, candidates, interests, rc)
	if err != nil {
		cc.Logger.Error("rss: LLMスコアリングに失敗、全候補を通知します", "error", err)
		return notifyChannel(ctx, cc, channelID, candidates, rc)
	}

	if len(scored) == 0 {
		cc.Logger.Debug("rss: 通知閾値を超える記事がありません", "channel", channelID)
		return nil
	}

	return notifyChannel(ctx, cc, channelID, scored, rc)
}

// userInterest represents a user's interest profile.
type userInterest struct {
	UserID      string
	Interests   string // concatenated interest texts
	Preferences string // RSS preferences (exclusions)
	Embedding   []float32
}

// queryUserInterests collects user interest data from memories.
func queryUserInterests(ctx context.Context, cc *scheduler.CronContext) ([]userInterest, error) {
	// Query user memories that have user_id in metadata.
	rows, err := cc.DB.QueryContext(ctx,
		`SELECT json_extract(metadata, '$.user_id'), content,
		        COALESCE(json_extract(metadata, '$.rss_preference'), 0) as is_pref
		 FROM memories
		 WHERE type = 'user' AND json_extract(metadata, '$.user_id') IS NOT NULL
		 ORDER BY json_extract(metadata, '$.user_id')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userMap := make(map[string]*userInterest)
	for rows.Next() {
		var userID, content string
		var isPref int
		if err := rows.Scan(&userID, &content, &isPref); err != nil {
			continue
		}
		ui, ok := userMap[userID]
		if !ok {
			ui = &userInterest{UserID: userID}
			userMap[userID] = ui
		}
		if isPref != 0 {
			if ui.Preferences != "" {
				ui.Preferences += "\n"
			}
			ui.Preferences += content
		} else {
			if ui.Interests != "" {
				ui.Interests += "\n"
			}
			ui.Interests += content
		}
	}

	var interests []userInterest
	for _, ui := range userMap {
		if ui.Interests == "" {
			continue
		}
		// Generate embedding for user interest profile.
		emb, err := cc.LLM.Embed(ctx, ui.Interests)
		if err != nil || len(emb) == 0 {
			continue
		}
		ui.Embedding = emb
		interests = append(interests, *ui)
	}

	return interests, nil
}

// vectorPreFilter filters items using cosine similarity against user interest vectors.
func vectorPreFilter(ctx context.Context, cc *scheduler.CronContext, items []itemWithFeed, interests []userInterest, threshold float64) []itemWithFeed {
	var candidates []itemWithFeed
	seen := make(map[string]bool)

	for _, iwf := range items {
		content := iwf.Item.Title
		if iwf.Item.Description != "" {
			content += "\n" + truncate(iwf.Item.Description, 300)
		}

		articleEmb, err := cc.LLM.Embed(ctx, content)
		if err != nil || len(articleEmb) == 0 {
			// Can't embed, include as candidate to be safe.
			if !seen[iwf.Item.ID] {
				candidates = append(candidates, iwf)
				seen[iwf.Item.ID] = true
			}
			continue
		}

		for _, ui := range interests {
			sim := cosineSimilarity(articleEmb, ui.Embedding)
			if sim >= threshold && !seen[iwf.Item.ID] {
				candidates = append(candidates, iwf)
				seen[iwf.Item.ID] = true
				break
			}
		}
	}

	return candidates
}

// llmBatchScore uses LLM to score candidate articles against user interests.
func llmBatchScore(ctx context.Context, cc *scheduler.CronContext, candidates []itemWithFeed, interests []userInterest, rc rssConfig) ([]itemWithFeed, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	prompt := buildScorePrompt(candidates, interests)

	messages := []providers.Message{
		{Role: "system", Content: "あなたは記事レコメンデーションスコアラーです。指示に従って各記事をスコアリングしてください。"},
		{Role: "user", Content: prompt},
	}

	resp, err := cc.LLM.CompleteRawDefault(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLMスコアリングに失敗: %w", err)
	}

	scores := parseScoreResponse(resp.Text)

	var result []itemWithFeed
	for i, iwf := range candidates {
		if i < len(scores) && scores[i].Score >= rc.NotifyThreshold {
			result = append(result, iwf)
		}
	}

	// Limit articles per notification.
	if len(result) > rc.MaxArticlesPerNotify {
		result = result[:rc.MaxArticlesPerNotify]
	}

	return result, nil
}

// buildScorePrompt creates the LLM prompt for batch article scoring.
func buildScorePrompt(candidates []itemWithFeed, interests []userInterest) string {
	var sb strings.Builder

	sb.WriteString("以下のユーザーの興味・除外設定と、新着RSS記事の一覧があります。\n")
	sb.WriteString("各記事について、ユーザー全体の興味に合っているかを0.0〜1.0でスコアリングしてください。\n")
	sb.WriteString("除外設定に該当する記事は必ず0.0にしてください。\n\n")

	sb.WriteString("## ユーザー興味\n")
	for _, ui := range interests {
		fmt.Fprintf(&sb, "- %s: %s\n", ui.UserID, truncate(ui.Interests, 200))
		if ui.Preferences != "" {
			fmt.Fprintf(&sb, "  除外: %s\n", truncate(ui.Preferences, 200))
		}
	}

	sb.WriteString("\n## 新着記事\n")
	for i, c := range candidates {
		desc := truncate(c.Item.Description, 100)
		fmt.Fprintf(&sb, "%d. \"%s\" (%s) - %s\n", i+1, c.Item.Title, c.Feed.Name, desc)
	}

	sb.WriteString("\n## 出力フォーマット (必ずこの形式で出力してください)\n")
	sb.WriteString("SCORES:\n")
	for i := range candidates {
		fmt.Fprintf(&sb, "- [%d] score=X.X reason=理由\n", i+1)
	}

	return sb.String()
}

// scoreResult holds a parsed score from LLM response.
type scoreResult struct {
	Index  int
	Score  float64
	Reason string
}

// parseScoreResponse extracts scores from the LLM response text.
func parseScoreResponse(text string) []scoreResult {
	re := regexp.MustCompile(`\[(\d+)\]\s*score=([0-9.]+)\s*reason=(.+)`)
	matches := re.FindAllStringSubmatch(text, -1)

	var results []scoreResult
	for _, m := range matches {
		idx, _ := strconv.Atoi(m[1])
		score, _ := strconv.ParseFloat(m[2], 64)
		results = append(results, scoreResult{
			Index:  idx,
			Score:  score,
			Reason: strings.TrimSpace(m[3]),
		})
	}

	// Sort by index to align with candidate order.
	// Index is 1-based in the prompt, convert to 0-based internally.
	aligned := make([]scoreResult, len(results))
	for i, r := range results {
		aligned[i] = r
		aligned[i].Index = r.Index - 1
	}

	return aligned
}

// notifyChannel sends a notification about new articles to a channel.
func notifyChannel(ctx context.Context, cc *scheduler.CronContext, channelID string, items []itemWithFeed, rc rssConfig) error {
	if len(items) == 0 {
		return nil
	}

	// Limit articles.
	if len(items) > rc.MaxArticlesPerNotify {
		items = items[:rc.MaxArticlesPerNotify]
	}

	// Use LLM to generate a natural notification message.
	message, err := generateNotification(ctx, cc, items)
	if err != nil {
		// Fallback: send a simple list.
		message = formatSimpleNotification(items)
	}

	if _, err := cc.Notifier.Send(ctx, channelID, message, "rss"); err != nil {
		return fmt.Errorf("通知の送信に失敗: %w", err)
	}

	// Mark items as notified.
	ids := make([]string, len(items))
	for i, iwf := range items {
		ids[i] = iwf.Item.ID
	}
	store := NewFeedStore(cc.DB)
	return store.MarkNotified(ctx, ids)
}

// generateNotification uses LLM to create a natural notification in suzuha's voice.
func generateNotification(ctx context.Context, cc *scheduler.CronContext, items []itemWithFeed) (string, error) {
	var sb strings.Builder
	sb.WriteString("あなたはsuzuhaです。以下の面白そうな新着記事をチャンネルに自然に共有してください。\n")
	sb.WriteString("あなたの感想も軽く添えてください。URLは必ず含めてください。\n")
	sb.WriteString("長すぎず、読みやすい形式でお願いします。\n\n")
	sb.WriteString("記事:\n")

	for i, iwf := range items {
		fmt.Fprintf(&sb, "%d. \"%s\" (%s)\n   %s\n   %s\n",
			i+1, iwf.Item.Title, iwf.Feed.Name,
			truncate(iwf.Item.Description, 150),
			iwf.Item.Link)
	}

	messages := []providers.Message{
		{Role: "system", Content: "あなたはsuzuhaというDiscordボットです。フレンドリーで好奇心旺盛な性格です。"},
		{Role: "user", Content: sb.String()},
	}

	resp, err := cc.LLM.CompleteRawDefault(ctx, messages)
	if err != nil {
		return "", err
	}
	text := llm.StripDirectiveTags(resp.Text)
	if text == "" {
		return "", fmt.Errorf("LLMが空の通知を返しました")
	}
	return text, nil
}

// formatSimpleNotification creates a plain text notification as fallback.
func formatSimpleNotification(items []itemWithFeed) string {
	var sb strings.Builder
	sb.WriteString("📰 新着記事をお知らせするよ！\n\n")
	for _, iwf := range items {
		fmt.Fprintf(&sb, "**%s** (%s)\n%s\n\n", iwf.Item.Title, iwf.Feed.Name, iwf.Item.Link)
	}
	return sb.String()
}

// --- RSS/Atom XML Parsing ---

// feedEntry is a normalized representation of an RSS or Atom article.
type feedEntry struct {
	Title       string
	Link        string
	GUID        string
	Description string
	Published   *time.Time
}

// RSS 2.0 structs
type rssXML struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// Atom structs
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string      `xml:"title"`
	Links     []atomLink  `xml:"link"`
	ID        string      `xml:"id"`
	Summary   string      `xml:"summary"`
	Content   atomContent `xml:"content"`
	Published string      `xml:"published"`
	Updated   string      `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomContent struct {
	Text string `xml:",chardata"`
}

// parseRSSOrAtom parses XML data as either RSS 2.0 or Atom feed.
func parseRSSOrAtom(data []byte) ([]feedEntry, error) {
	// Try RSS 2.0 first.
	var rss rssXML
	if err := xml.Unmarshal(data, &rss); err == nil && len(rss.Channel.Items) > 0 {
		return convertRSSItems(rss.Channel.Items), nil
	}

	// Try Atom.
	var atom atomFeed
	if err := xml.Unmarshal(data, &atom); err == nil && len(atom.Entries) > 0 {
		return convertAtomEntries(atom.Entries), nil
	}

	return nil, fmt.Errorf("認識できないフィード形式です")
}

func convertRSSItems(items []rssItem) []feedEntry {
	entries := make([]feedEntry, 0, len(items))
	for _, item := range items {
		entry := feedEntry{
			Title:       item.Title,
			Link:        item.Link,
			GUID:        item.GUID,
			Description: item.Description,
		}
		if item.PubDate != "" {
			if t, err := parseRSSDate(item.PubDate); err == nil {
				entry.Published = &t
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func convertAtomEntries(atomEntries []atomEntry) []feedEntry {
	entries := make([]feedEntry, 0, len(atomEntries))
	for _, ae := range atomEntries {
		entry := feedEntry{
			Title: ae.Title,
			GUID:  ae.ID,
		}

		// Get the best link: prefer rel="alternate", fall back to first.
		for _, l := range ae.Links {
			if l.Rel == "alternate" || l.Rel == "" {
				entry.Link = l.Href
				break
			}
		}
		if entry.Link == "" && len(ae.Links) > 0 {
			entry.Link = ae.Links[0].Href
		}

		// Prefer content over summary.
		if ae.Content.Text != "" {
			entry.Description = ae.Content.Text
		} else {
			entry.Description = ae.Summary
		}

		dateStr := ae.Published
		if dateStr == "" {
			dateStr = ae.Updated
		}
		if dateStr != "" {
			if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
				entry.Published = &t
			}
		}

		entries = append(entries, entry)
	}
	return entries
}

// parseRSSDate tries multiple common RSS date formats.
func parseRSSDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC3339,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("認識できない日付形式: %s", s)
}

// --- Utility functions ---

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// truncate shortens a string to maxRunes runes.
func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
