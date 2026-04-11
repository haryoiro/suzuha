package transcript

import (
	"net/url"
	"regexp"
	"strings"
)

// YouTube の URL パターン。
var youtubePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:youtube\.com/watch\?.*v=|youtu\.be/|youtube\.com/shorts/)([a-zA-Z0-9_-]{11})`),
}

// yt-dlp が対応する動画サイトの URL パターン (主要なもの)。
var videoPlatformHosts = map[string]bool{
	"www.youtube.com":    true,
	"youtube.com":        true,
	"youtu.be":           true,
	"www.nicovideo.jp":   true,
	"nicovideo.jp":       true,
	"www.twitch.tv":      true,
	"twitch.tv":          true,
	"clips.twitch.tv":    true,
	"www.bilibili.com":   true,
	"bilibili.com":       true,
	"www.dailymotion.com": true,
	"vimeo.com":          true,
}

// ExtractYouTubeID は URL から YouTube の video ID を抽出する。
// YouTube URL でなければ空文字を返す。
func ExtractYouTubeID(rawURL string) string {
	for _, p := range youtubePatterns {
		if m := p.FindStringSubmatch(rawURL); len(m) >= 2 {
			return m[1]
		}
	}
	return ""
}

// IsVideoURL は URL が動画プラットフォームのものか判定する。
func IsVideoURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return videoPlatformHosts[host]
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// ExtractVideoURLs はテキスト中の動画 URL を全て抽出する。
func ExtractVideoURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)
	var result []string
	for _, m := range matches {
		if IsVideoURL(m) {
			result = append(result, m)
		}
	}
	return result
}
