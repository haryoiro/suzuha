package twitter

import (
	"testing"
)

func TestIsTwitterURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://x.com/jack/status/20", true},
		{"https://twitter.com/jack/status/20", true},
		{"https://www.x.com/user/status/123456", true},
		{"https://x.com/user/status/123?s=20", true},
		{"https://x.com/user", false},
		{"https://youtube.com/watch?v=xxx", false},
		{"https://example.com", false},
	}
	for _, tt := range tests {
		got := IsTwitterURL(tt.url)
		if got != tt.want {
			t.Errorf("IsTwitterURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestExtractTweetID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://x.com/jack/status/20", "20"},
		{"https://twitter.com/user/status/1234567890123456789", "1234567890123456789"},
		{"https://x.com/user/status/123?s=20&t=abc", "123"},
		{"https://x.com/user", ""},
		{"https://example.com", ""},
	}
	for _, tt := range tests {
		got := ExtractTweetID(tt.url)
		if got != tt.want {
			t.Errorf("ExtractTweetID(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestExtractTwitterURLs(t *testing.T) {
	text := "見て https://x.com/jack/status/20 と https://example.com と https://twitter.com/user/status/123"
	urls := ExtractTwitterURLs(text)
	if len(urls) != 2 {
		t.Fatalf("got %d URLs, want 2: %v", len(urls), urls)
	}
}

func TestFormatTweet(t *testing.T) {
	tw := &Tweet{
		Text:       "hello world",
		AuthorName: "Jack",
		AuthorID:   "jack",
		Likes:      100,
		Retweets:   50,
		Replies:    10,
	}
	result := FormatTweet(tw)
	if result == "" {
		t.Error("empty result")
	}
	if !containsStr(result, "@jack") || !containsStr(result, "hello world") {
		t.Errorf("unexpected format: %s", result)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
