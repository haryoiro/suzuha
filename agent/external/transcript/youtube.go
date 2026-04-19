package transcript

import (
	"context"
	"fmt"

	yt "github.com/horiagug/youtube-transcript-api-go/pkg/yt_transcript"
)

// YouTubeFetcher は Go ライブラリで YouTube の字幕を取得する。
// YouTube 内部 API を直接叩くため、API キー不要。
type YouTubeFetcher struct {
	client *yt.YtTranscriptClient
}

// NewYouTubeFetcher は YouTubeFetcher を作成する。
func NewYouTubeFetcher() *YouTubeFetcher {
	return &YouTubeFetcher{
		client: yt.NewClient(),
	}
}

// Supports は YouTube URL のみサポート。
func (f *YouTubeFetcher) Supports(url string) bool {
	return ExtractYouTubeID(url) != ""
}

// Fetch は YouTube の字幕を取得する。
func (f *YouTubeFetcher) Fetch(ctx context.Context, rawURL string, langs []string) (VideoInfo, []Line, error) {
	videoID := ExtractYouTubeID(rawURL)
	if videoID == "" {
		return VideoInfo{}, nil, fmt.Errorf("youtube: 動画 ID を抽出できません: %s", rawURL)
	}

	if len(langs) == 0 {
		langs = []string{"ja", "en"}
	}

	transcripts, err := f.client.GetTranscripts(videoID, langs)
	if err != nil {
		return VideoInfo{}, nil, fmt.Errorf("youtube: 字幕取得に失敗: %w", err)
	}

	if len(transcripts) == 0 {
		return VideoInfo{}, nil, fmt.Errorf("youtube: 字幕が見つかりません: %s", videoID)
	}

	t := transcripts[0]
	info := VideoInfo{
		Title: t.VideoTitle,
	}

	lines := make([]Line, len(t.Lines))
	for i, l := range t.Lines {
		lines[i] = Line{
			Text:     l.Text,
			Start:    l.Start,
			Duration: l.Duration,
		}
		// 最後の行の終了時点を Duration として使用
		if end := l.Start + l.Duration; end > info.Duration {
			info.Duration = end
		}
	}

	return info, lines, nil
}

// FetchMetadata は YouTube のメタデータだけを取得する。
// 実際には字幕を取得してタイトルと長さだけ返す (軽量取得 API がないため)。
func (f *YouTubeFetcher) FetchMetadata(ctx context.Context, rawURL string) (VideoInfo, error) {
	info, _, err := f.Fetch(ctx, rawURL, []string{"ja", "en"})
	if err != nil {
		return VideoInfo{}, err
	}
	return info, nil
}
