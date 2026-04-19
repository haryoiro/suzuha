package video

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/haryoiro/suzuha/internal/adapter/transcript"
)

// AnnotateVideoURLs はメッセージ中の動画 URL を検知し、メタデータで enrich する。
// Perceive から呼ばれる。
//
// 例:
//
//	"この動画面白い https://youtube.com/watch?v=xxx"
//	→ "この動画面白い [動画: "タイトル" (3:42) | video_watch で視聴可能] https://youtube.com/watch?v=xxx"
func AnnotateVideoURLs(ctx context.Context, text string, meta transcript.MetadataFetcher, logger *slog.Logger) string {
	if meta == nil {
		return text
	}

	urls := transcript.ExtractVideoURLs(text)
	if len(urls) == 0 {
		return text
	}

	for _, u := range urls {
		if !meta.Supports(u) {
			continue
		}

		info, err := meta.FetchMetadata(ctx, u)
		if err != nil {
			logger.Debug("video: メタデータ取得失敗", "url", u, "error", err)
			continue
		}

		durMin := int(info.Duration) / 60
		durSec := int(info.Duration) % 60
		annotation := fmt.Sprintf("[動画: %q (%d:%02d) | video_watch で視聴可能] ", info.Title, durMin, durSec)

		text = strings.Replace(text, u, annotation+u, 1)
	}

	return text
}
