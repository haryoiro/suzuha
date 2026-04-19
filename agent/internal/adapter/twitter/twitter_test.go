package twitter

import (
	"strings"
	"testing"
)

func TestIsTwitterURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"x.com status", "https://x.com/jack/status/20", true},
		{"twitter.com status", "https://twitter.com/jack/status/20", true},
		{"www.x.com status", "https://www.x.com/user/status/123456", true},
		{"x.com with query params", "https://x.com/user/status/123?s=20", true},
		{"x.com profile (no status)", "https://x.com/user", false},
		{"YouTube URL", "https://youtube.com/watch?v=xxx", false},
		{"unrelated URL", "https://example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTwitterURL(tt.url); got != tt.want {
				t.Errorf("IsTwitterURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractTweetID(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"x.com short ID", "https://x.com/jack/status/20", "20"},
		{"twitter.com long ID", "https://twitter.com/user/status/1234567890123456789", "1234567890123456789"},
		{"URL with query params", "https://x.com/user/status/123?s=20&t=abc", "123"},
		{"profile URL", "https://x.com/user", ""},
		{"unrelated URL", "https://example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTweetID(tt.url); got != tt.want {
				t.Errorf("ExtractTweetID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractTwitterURLs(t *testing.T) {
	t.Run("extracts Twitter URLs from text", func(t *testing.T) {
		text := "見て https://x.com/jack/status/20 と https://example.com と https://twitter.com/user/status/123"
		urls := ExtractTwitterURLs(text)
		if len(urls) != 2 {
			t.Fatalf("got %d URLs, want 2: %v", len(urls), urls)
		}
	})
}

func TestFormatTweet(t *testing.T) {
	t.Run("formats tweet correctly", func(t *testing.T) {
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
		if !strings.Contains(result, "@jack") || !strings.Contains(result, "hello world") {
			t.Errorf("unexpected format: %s", result)
		}
	})
}
