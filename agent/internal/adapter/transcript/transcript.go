// Package transcript は動画の字幕テキストを取得する。
// 複数の実装 (Go ライブラリ, yt-dlp CLI) をフォールバックチェーンで切り替え可能。
package transcript

import (
	"context"
	"fmt"
	"log/slog"
)

// Line はタイムスタンプ付きの字幕1行。
type Line struct {
	Text     string  // 字幕テキスト
	Start    float64 // 開始秒
	Duration float64 // 長さ (秒)
}

// VideoInfo は動画のメタデータ。
type VideoInfo struct {
	Title    string  // 動画タイトル
	Duration float64 // 全体の長さ (秒)
}

// Fetcher は動画の字幕テキストを取得する。
type Fetcher interface {
	// Supports はこの fetcher が指定 URL をサポートするか返す。
	Supports(url string) bool
	// Fetch は動画の字幕を取得する。
	// 字幕が取得できない場合は error を返す。
	Fetch(ctx context.Context, url string, langs []string) (VideoInfo, []Line, error)
}

// MetadataFetcher は動画のメタデータだけを軽量に取得する (Perceive 用)。
type MetadataFetcher interface {
	Supports(url string) bool
	FetchMetadata(ctx context.Context, url string) (VideoInfo, error)
}

// Chain は複数の Fetcher を順に試すフォールバックチェーン。
type Chain struct {
	fetchers []Fetcher
	logger   *slog.Logger
}

// NewChain はフォールバックチェーンを作成する。
func NewChain(logger *slog.Logger, fetchers ...Fetcher) *Chain {
	return &Chain{fetchers: fetchers, logger: logger}
}

// Fetch は対応する Fetcher を順に試し、最初に成功した結果を返す。
func (c *Chain) Fetch(ctx context.Context, url string, langs []string) (VideoInfo, []Line, error) {
	var lastErr error
	for _, f := range c.fetchers {
		if !f.Supports(url) {
			continue
		}
		info, lines, err := f.Fetch(ctx, url, langs)
		if err == nil {
			return info, lines, nil
		}
		lastErr = err
		c.logger.Debug("transcript fetcher failed, trying next", "error", err)
	}
	if lastErr == nil {
		return VideoInfo{}, nil, fmt.Errorf("transcript: URL に対応する fetcher がありません: %s", url)
	}
	return VideoInfo{}, nil, fmt.Errorf("transcript: all fetchers failed: %w", lastErr)
}

// Supports は少なくとも1つの Fetcher が対応していれば true を返す。
func (c *Chain) Supports(url string) bool {
	for _, f := range c.fetchers {
		if f.Supports(url) {
			return true
		}
	}
	return false
}

// FormatTranscript は Line スライスを LLM 用のテキストにフォーマットする。
func FormatTranscript(info VideoInfo, lines []Line, maxLen int) string {
	durMin := int(info.Duration) / 60
	durSec := int(info.Duration) % 60

	result := fmt.Sprintf("[動画] タイトル: %s\n[動画] 長さ: %d:%02d\n\n", info.Title, durMin, durSec)

	for _, l := range lines {
		min := int(l.Start) / 60
		sec := int(l.Start) % 60
		line := fmt.Sprintf("[%02d:%02d] %s\n", min, sec, l.Text)
		if maxLen > 0 && len(result)+len(line) > maxLen {
			result += "\n... (字幕が長いため省略)"
			break
		}
		result += line
	}
	return result
}
