package transcript

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestExtractYouTubeID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=abc123_-XYZ&t=120", "abc123_-XYZ"},
		{"https://example.com", ""},
		{"not a url", ""},
	}
	for _, tt := range tests {
		got := ExtractYouTubeID(tt.url)
		if got != tt.want {
			t.Errorf("ExtractYouTubeID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestIsVideoURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=xxx", true},
		{"https://youtu.be/xxx", true},
		{"https://www.nicovideo.jp/watch/sm1234", true},
		{"https://www.twitch.tv/videos/123", true},
		{"https://clips.twitch.tv/xxx", true},
		{"https://www.bilibili.com/video/BV1xx", true},
		{"https://vimeo.com/123", true},
		{"https://example.com", false},
		{"https://google.com", false},
	}
	for _, tt := range tests {
		got := IsVideoURL(tt.url)
		if got != tt.want {
			t.Errorf("IsVideoURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestExtractVideoURLs(t *testing.T) {
	text := "この動画面白い https://youtube.com/watch?v=abc と https://example.com と https://www.nicovideo.jp/watch/sm123"
	urls := ExtractVideoURLs(text)
	if len(urls) != 2 {
		t.Fatalf("got %d URLs, want 2: %v", len(urls), urls)
	}
	if !strings.Contains(urls[0], "youtube.com") {
		t.Errorf("urls[0] = %q, want youtube", urls[0])
	}
	if !strings.Contains(urls[1], "nicovideo.jp") {
		t.Errorf("urls[1] = %q, want nicovideo", urls[1])
	}
}

type mockFetcher struct {
	supports bool
	info     VideoInfo
	lines    []Line
	err      error
}

func (m *mockFetcher) Supports(url string) bool { return m.supports }
func (m *mockFetcher) Fetch(ctx context.Context, url string, langs []string) (VideoInfo, []Line, error) {
	return m.info, m.lines, m.err
}

func TestChain_FirstSuccess(t *testing.T) {
	chain := NewChain(slog.Default(),
		&mockFetcher{supports: true, info: VideoInfo{Title: "first"}, lines: []Line{{Text: "hello"}}},
		&mockFetcher{supports: true, info: VideoInfo{Title: "second"}},
	)
	info, lines, err := chain.Fetch(context.Background(), "https://youtube.com/watch?v=xxx", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "first" {
		t.Errorf("first fetcher の結果が返るべき, got %q", info.Title)
	}
	if len(lines) != 1 {
		t.Errorf("lines = %d, want 1", len(lines))
	}
}

func TestChain_Fallback(t *testing.T) {
	chain := NewChain(slog.Default(),
		&mockFetcher{supports: true, err: fmt.Errorf("fail")},
		&mockFetcher{supports: true, info: VideoInfo{Title: "fallback"}, lines: []Line{{Text: "ok"}}},
	)
	info, _, err := chain.Fetch(context.Background(), "https://youtube.com/watch?v=xxx", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "fallback" {
		t.Errorf("fallback fetcher の結果が返るべき, got %q", info.Title)
	}
}

func TestChain_SkipsUnsupported(t *testing.T) {
	chain := NewChain(slog.Default(),
		&mockFetcher{supports: false, info: VideoInfo{Title: "skip"}},
		&mockFetcher{supports: true, info: VideoInfo{Title: "match"}, lines: []Line{{Text: "ok"}}},
	)
	info, _, err := chain.Fetch(context.Background(), "https://youtube.com/watch?v=xxx", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "match" {
		t.Errorf("supports=true の fetcher が使われるべき, got %q", info.Title)
	}
}

func TestChain_AllFail(t *testing.T) {
	chain := NewChain(slog.Default(),
		&mockFetcher{supports: true, err: fmt.Errorf("err1")},
		&mockFetcher{supports: true, err: fmt.Errorf("err2")},
	)
	_, _, err := chain.Fetch(context.Background(), "https://youtube.com/watch?v=xxx", nil)
	if err == nil {
		t.Error("全 fetcher 失敗時はエラーを返すべき")
	}
}

func TestChain_NoSupported(t *testing.T) {
	chain := NewChain(slog.Default(),
		&mockFetcher{supports: false},
	)
	_, _, err := chain.Fetch(context.Background(), "https://unknown.com/video", nil)
	if err == nil {
		t.Error("対応 fetcher なしはエラーを返すべき")
	}
}

func TestFormatTranscript(t *testing.T) {
	info := VideoInfo{Title: "テスト動画", Duration: 125}
	lines := []Line{
		{Text: "こんにちは", Start: 0, Duration: 3},
		{Text: "今日は天気がいい", Start: 3, Duration: 5},
		{Text: "終わり", Start: 63, Duration: 2},
	}
	result := FormatTranscript(info, lines, 0)
	if !strings.Contains(result, "テスト動画") {
		t.Error("タイトルが含まれるべき")
	}
	if !strings.Contains(result, "2:05") {
		t.Error("長さ 2:05 が含まれるべき")
	}
	if !strings.Contains(result, "[00:00]") || !strings.Contains(result, "[01:03]") {
		t.Error("タイムスタンプが含まれるべき")
	}
}

func TestFormatTranscript_Truncate(t *testing.T) {
	info := VideoInfo{Title: "長い動画", Duration: 3600}
	lines := make([]Line, 100)
	for i := range lines {
		lines[i] = Line{Text: "これは長いテキスト行です。" + strings.Repeat("あ", 50), Start: float64(i * 30)}
	}
	result := FormatTranscript(info, lines, 500)
	if !strings.Contains(result, "省略") {
		t.Error("truncate 時は省略メッセージが含まれるべき")
	}
}
