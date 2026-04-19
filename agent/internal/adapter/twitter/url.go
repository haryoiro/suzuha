package twitter

import (
	"net/url"
	"regexp"
	"strings"
)

var twitterHosts = map[string]bool{
	"x.com":          true,
	"www.x.com":      true,
	"twitter.com":    true,
	"www.twitter.com": true,
}

// tweetIDPattern はツイート URL からステータス ID を抽出する。
var tweetIDPattern = regexp.MustCompile(`/status/(\d+)`)

// IsTwitterURL は URL が X/Twitter の投稿 URL か判定する。
func IsTwitterURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !twitterHosts[host] {
		return false
	}
	return tweetIDPattern.MatchString(u.Path)
}

// ExtractTweetID は URL からツイート ID を抽出する。
func ExtractTweetID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	m := tweetIDPattern.FindStringSubmatch(u.Path)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// ExtractTwitterURLs はテキスト中の X/Twitter URL を全て抽出する。
func ExtractTwitterURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)
	var result []string
	for _, m := range matches {
		if IsTwitterURL(m) {
			result = append(result, m)
		}
	}
	return result
}
