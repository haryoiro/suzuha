package transcript

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// FrameExtractor は動画の特定時点のフレームを JPEG として取得する。
type FrameExtractor interface {
	ExtractFrame(ctx context.Context, url string, timestampSec float64) ([]byte, error)
}

// YtDlpFrameExtractor は yt-dlp + ffmpeg で動画フレームを切り出す。
type YtDlpFrameExtractor struct {
	ytdlpBin  string
	ffmpegBin string
}

// NewYtDlpFrameExtractor は YtDlpFrameExtractor を作成する。
func NewYtDlpFrameExtractor() *YtDlpFrameExtractor {
	return &YtDlpFrameExtractor{ytdlpBin: "yt-dlp", ffmpegBin: "ffmpeg"}
}

// ExtractFrame は yt-dlp で直接ストリーム URL を取得し、ffmpeg で指定時点の JPEG フレームを切り出す。
func (e *YtDlpFrameExtractor) ExtractFrame(ctx context.Context, rawURL string, timestampSec float64) ([]byte, error) {
	// 1. yt-dlp で直接ストリーム URL を取得 (720p 以下を優先)
	streamCmd := exec.CommandContext(ctx, e.ytdlpBin,
		"-g",
		"--format", "best[height<=720]/best",
		rawURL,
	)
	streamOut, err := streamCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("frame: ストリーム URL 取得に失敗: %w", err)
	}

	streamURL := string(bytes.TrimSpace(streamOut))
	if streamURL == "" {
		return nil, fmt.Errorf("frame: ストリーム URL が空です")
	}

	// 複数行の場合 (video + audio 別々) は最初の行を使う
	lines := bytes.Split(bytes.TrimSpace(streamOut), []byte("\n"))
	streamURL = string(bytes.TrimSpace(lines[0]))

	// 2. ffmpeg で指定時点のフレームを JPEG として stdout に出力
	ts := strconv.FormatFloat(timestampSec, 'f', 2, 64)
	ffmpegCmd := exec.CommandContext(ctx, e.ffmpegBin,
		"-ss", ts,
		"-i", streamURL,
		"-frames:v", "1",
		"-f", "image2",
		"-vcodec", "mjpeg",
		"-q:v", "3",
		"pipe:1",
	)

	var stdout, stderr bytes.Buffer
	ffmpegCmd.Stdout = &stdout
	ffmpegCmd.Stderr = &stderr

	if err := ffmpegCmd.Run(); err != nil {
		return nil, fmt.Errorf("frame: ffmpeg 失敗: %w (stderr: %s)", err, stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("frame: フレームが空です (timestamp=%.2fs)", timestampSec)
	}

	return stdout.Bytes(), nil
}
