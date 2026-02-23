package tasks

import (
	"strings"
	"testing"
)

func TestParseRSSOrAtom_RSS(t *testing.T) {
	rssXML := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Blog</title>
    <item>
      <title>First Post</title>
      <link>https://example.com/post1</link>
      <guid>guid-001</guid>
      <description>This is the first post.</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate>
    </item>
    <item>
      <title>Second Post</title>
      <link>https://example.com/post2</link>
      <guid>guid-002</guid>
      <description>This is the second post.</description>
    </item>
  </channel>
</rss>`

	entries, err := parseRSSOrAtom([]byte(rssXML))
	if err != nil {
		t.Fatalf("parse rss: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Title != "First Post" {
		t.Errorf("title: got %q", entries[0].Title)
	}
	if entries[0].Link != "https://example.com/post1" {
		t.Errorf("link: got %q", entries[0].Link)
	}
	if entries[0].GUID != "guid-001" {
		t.Errorf("guid: got %q", entries[0].GUID)
	}
	if entries[0].Description != "This is the first post." {
		t.Errorf("description: got %q", entries[0].Description)
	}
	if entries[0].Published == nil {
		t.Fatal("expected published date")
	}

	if entries[1].Title != "Second Post" {
		t.Errorf("second title: got %q", entries[1].Title)
	}
	if entries[1].Published != nil {
		t.Errorf("expected nil published for second entry")
	}
}

func TestParseRSSOrAtom_Atom(t *testing.T) {
	atomXML := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Go Blog</title>
  <entry>
    <title>Go 1.23 Released</title>
    <link href="https://go.dev/blog/go1.23" rel="alternate"/>
    <id>tag:go.dev,2024:go1.23</id>
    <summary>Go 1.23 is released with new features.</summary>
    <published>2024-08-01T00:00:00Z</published>
  </entry>
  <entry>
    <title>Go Modules</title>
    <link href="https://go.dev/blog/modules"/>
    <id>tag:go.dev,2024:modules</id>
    <content>Detailed content about Go modules.</content>
    <updated>2024-07-15T12:00:00Z</updated>
  </entry>
</feed>`

	entries, err := parseRSSOrAtom([]byte(atomXML))
	if err != nil {
		t.Fatalf("parse atom: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Title != "Go 1.23 Released" {
		t.Errorf("title: got %q", entries[0].Title)
	}
	if entries[0].Link != "https://go.dev/blog/go1.23" {
		t.Errorf("link: got %q", entries[0].Link)
	}
	if entries[0].GUID != "tag:go.dev,2024:go1.23" {
		t.Errorf("guid: got %q", entries[0].GUID)
	}
	if entries[0].Description != "Go 1.23 is released with new features." {
		t.Errorf("description: got %q", entries[0].Description)
	}

	// Second entry uses content instead of summary.
	if entries[1].Description != "Detailed content about Go modules." {
		t.Errorf("description: got %q", entries[1].Description)
	}
	// Second entry uses updated instead of published.
	if entries[1].Published == nil {
		t.Fatal("expected published date from updated field")
	}
}

func TestParseRSSOrAtom_Invalid(t *testing.T) {
	_, err := parseRSSOrAtom([]byte("not xml"))
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if sim < 0.999 {
		t.Errorf("identical vectors: expected ~1.0, got %f", sim)
	}

	c := []float32{0, 1, 0}
	sim2 := cosineSimilarity(a, c)
	if sim2 > 0.001 {
		t.Errorf("orthogonal vectors: expected ~0.0, got %f", sim2)
	}

	d := []float32{-1, 0, 0}
	sim3 := cosineSimilarity(a, d)
	if sim3 > -0.999 {
		t.Errorf("opposite vectors: expected ~-1.0, got %f", sim3)
	}

	// Different lengths.
	e := []float32{1, 0}
	sim4 := cosineSimilarity(a, e)
	if sim4 != 0 {
		t.Errorf("different lengths: expected 0, got %f", sim4)
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != "hello" {
		t.Errorf("short string: got %q", truncate(short, 10))
	}

	long := "hello world"
	got := truncate(long, 5)
	if got != "hello..." {
		t.Errorf("long string: got %q", got)
	}

	// Japanese characters.
	jp := "これはテストです"
	got2 := truncate(jp, 4)
	if got2 != "これはテ..." {
		t.Errorf("japanese: got %q", got2)
	}
}

func TestParseScoreResponse(t *testing.T) {
	text := `SCORES:
- [1] score=0.9 reason=Goプログラミングに直結
- [2] score=0.0 reason=チェンジログで除外設定に該当
- [3] score=0.7 reason=機械学習に関連`

	results := parseScoreResponse(text)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Index != 0 { // 1-based → 0-based
		t.Errorf("index[0]: got %d", results[0].Index)
	}
	if results[0].Score != 0.9 {
		t.Errorf("score[0]: got %f", results[0].Score)
	}
	if results[1].Score != 0.0 {
		t.Errorf("score[1]: got %f", results[1].Score)
	}
	if results[2].Score != 0.7 {
		t.Errorf("score[2]: got %f", results[2].Score)
	}
}

func TestParseRSSDate(t *testing.T) {
	tests := []struct {
		input string
		ok    bool
	}{
		{"Mon, 02 Jan 2006 15:04:05 +0000", true},
		{"Mon, 02 Jan 2006 15:04:05 MST", true},
		{"2024-08-01T00:00:00Z", true},
		{"2024-01-15", true},
		{"not a date", false},
	}
	for _, tc := range tests {
		_, err := parseRSSDate(tc.input)
		if (err == nil) != tc.ok {
			t.Errorf("parseRSSDate(%q): err=%v, want ok=%v", tc.input, err, tc.ok)
		}
	}
}

func TestDefaultRSSConfig(t *testing.T) {
	rc := defaultRSSConfig()
	if rc.VectorThreshold != 0.3 {
		t.Errorf("vector_threshold: got %f", rc.VectorThreshold)
	}
	if rc.NotifyThreshold != 0.6 {
		t.Errorf("notify_threshold: got %f", rc.NotifyThreshold)
	}
	if rc.MaxArticlesPerNotify != 5 {
		t.Errorf("max_articles: got %d", rc.MaxArticlesPerNotify)
	}
}

func TestBuildScorePrompt(t *testing.T) {
	candidates := []itemWithFeed{
		{
			Item: Item{Title: "Go 1.23 Released", Description: "New features in Go 1.23"},
			Feed: Feed{Name: "Go Blog"},
		},
	}
	interests := []userInterest{
		{UserID: "user1", Interests: "Go programming", Preferences: "チェンジログ除外"},
	}

	prompt := buildScorePrompt(candidates, interests)

	if !strings.Contains(prompt, "user1") {
		t.Error("prompt should contain user ID")
	}
	if !strings.Contains(prompt, "Go programming") {
		t.Error("prompt should contain interests")
	}
	if !strings.Contains(prompt, "チェンジログ除外") {
		t.Error("prompt should contain preferences")
	}
	if !strings.Contains(prompt, "Go 1.23 Released") {
		t.Error("prompt should contain article title")
	}
	if !strings.Contains(prompt, "SCORES:") {
		t.Error("prompt should contain output format")
	}
}

func TestRSSTask_NameAndDescription(t *testing.T) {
	task := &RSSTask{}
	if task.Name() != "rss" {
		t.Errorf("name: got %q", task.Name())
	}
	if task.Description() == "" {
		t.Error("description should not be empty")
	}
}

func TestConvertAtomEntries_PreferContent(t *testing.T) {
	entries := convertAtomEntries([]atomEntry{
		{
			Title:   "Test",
			ID:      "id-1",
			Summary: "summary text",
			Content: atomContent{Text: "full content text"},
			Links:   []atomLink{{Href: "https://example.com", Rel: "alternate"}},
		},
	})

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Description != "full content text" {
		t.Errorf("expected content over summary, got %q", entries[0].Description)
	}
}

func TestConvertAtomEntries_FallbackLink(t *testing.T) {
	entries := convertAtomEntries([]atomEntry{
		{
			Title: "Test",
			ID:    "id-1",
			Links: []atomLink{{Href: "https://example.com/other", Rel: "enclosure"}},
		},
	})

	if entries[0].Link != "https://example.com/other" {
		t.Errorf("expected fallback to first link, got %q", entries[0].Link)
	}
}

func TestFormatSimpleNotification(t *testing.T) {
	items := []itemWithFeed{
		{
			Item: Item{Title: "Test Article", Link: "https://example.com/test"},
			Feed: Feed{Name: "Test Feed"},
		},
	}
	msg := formatSimpleNotification(items)
	if !strings.Contains(msg, "Test Article") {
		t.Error("notification should contain article title")
	}
	if !strings.Contains(msg, "https://example.com/test") {
		t.Error("notification should contain article link")
	}
}
