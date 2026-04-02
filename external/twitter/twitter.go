// Package twitter は X (Twitter) の投稿コンテンツを取得する。
// FxTwitter API を使用し、API キー不要で動作する。
package twitter

import "context"

// Tweet は X の投稿データ。
type Tweet struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	AuthorName string   `json:"author_name"`
	AuthorID   string   `json:"author_id"`   // @screen_name
	Images     []string `json:"images"`      // 画像 URL
	VideoURL   string   `json:"video_url"`   // 動画 URL (あれば)
	CreatedAt  string   `json:"created_at"`
	Likes      int      `json:"likes"`
	Retweets   int      `json:"retweets"`
	Replies    int      `json:"replies"`
	URL        string   `json:"url"`
}

// Fetcher は X の投稿を取得する。
type Fetcher interface {
	Supports(url string) bool
	Fetch(ctx context.Context, url string) (*Tweet, error)
}

// FormatTweet は Tweet を LLM 用のテキストにフォーマットする。
func FormatTweet(t *Tweet) string {
	result := "[Tweet] @" + t.AuthorID + " (" + t.AuthorName + ")\n"
	result += t.Text + "\n"
	if len(t.Images) > 0 {
		result += "[画像 " + itoa(len(t.Images)) + "枚]\n"
	}
	if t.VideoURL != "" {
		result += "[動画あり]\n"
	}
	result += "♡ " + itoa(t.Likes) + "  ↻ " + itoa(t.Retweets) + "  💬 " + itoa(t.Replies)
	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
