package transcript

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// YtDlpFetcher は yt-dlp CLI で字幕を取得する。
// YouTube 以外のプラットフォーム (ニコニコ, Twitch 等) にも対応。
// yt-dlp バイナリがシステムに必要。
type YtDlpFetcher struct {
	bin string // yt-dlp バイナリパス (デフォルト: "yt-dlp")
}

// NewYtDlpFetcher は YtDlpFetcher を作成する。
func NewYtDlpFetcher() *YtDlpFetcher {
	return &YtDlpFetcher{bin: "yt-dlp"}
}

// Supports は yt-dlp が対応するプラットフォームか判定する。
func (f *YtDlpFetcher) Supports(url string) bool {
	return IsVideoURL(url)
}

// Fetch は yt-dlp で字幕を取得する。
func (f *YtDlpFetcher) Fetch(ctx context.Context, rawURL string, langs []string) (VideoInfo, []Line, error) {
	if len(langs) == 0 {
		langs = []string{"ja", "en"}
	}

	// メタデータ取得
	info, err := f.FetchMetadata(ctx, rawURL)
	if err != nil {
		return VideoInfo{}, nil, err
	}

	// 字幕取得 (自動生成含む)
	langArg := strings.Join(langs, ",")
	cmd := exec.CommandContext(ctx, f.bin,
		"--write-auto-sub",
		"--sub-lang", langArg,
		"--sub-format", "vtt",
		"--skip-download",
		"--print-to-file", "%(requested_subtitles)j", "/dev/stderr",
		"-o", "-",
		"--write-sub",
		rawURL,
	)

	// VTT を stdout に出力させる
	cmd = exec.CommandContext(ctx, f.bin,
		"--write-auto-sub",
		"--write-sub",
		"--sub-lang", langArg,
		"--convert-subs", "vtt",
		"--skip-download",
		"-o", "/tmp/yt-sub-%(id)s",
		rawURL,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return VideoInfo{}, nil, fmt.Errorf("ytdlp: 字幕取得に失敗: %w (output: %s)", err, string(output))
	}

	// yt-dlp が保存した VTT ファイルを探す
	lines, err := f.findAndParseVTT(ctx, rawURL, langs)
	if err != nil {
		return VideoInfo{}, nil, err
	}

	return info, lines, nil
}

// FetchMetadata は yt-dlp --dump-json でメタデータだけ取得する。
func (f *YtDlpFetcher) FetchMetadata(ctx context.Context, rawURL string) (VideoInfo, error) {
	cmd := exec.CommandContext(ctx, f.bin, "--dump-json", "--skip-download", rawURL)
	output, err := cmd.Output()
	if err != nil {
		return VideoInfo{}, fmt.Errorf("ytdlp: メタデータ取得に失敗: %w", err)
	}

	var meta struct {
		Title    string  `json:"title"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(output, &meta); err != nil {
		return VideoInfo{}, fmt.Errorf("ytdlp: JSON パースに失敗: %w", err)
	}

	return VideoInfo{Title: meta.Title, Duration: meta.Duration}, nil
}

// findAndParseVTT は yt-dlp が保存した VTT ファイルを読んでパ��スする。
func (f *YtDlpFetcher) findAndParseVTT(ctx context.Context, rawURL string, langs []string) ([]Line, error) {
	// video ID を取得して VTT ファイルパスを推測
	cmd := exec.CommandContext(ctx, f.bin, "--print", "id", rawURL)
	idOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp: ID 取得に失敗: %w", err)
	}
	videoID := strings.TrimSpace(string(idOut))

	// 言語順に VTT ファイルを探す
	for _, lang := range langs {
		path := fmt.Sprintf("/tmp/yt-sub-%s.%s.vtt", videoID, lang)
		lines, err := parseVTTFile(path)
		if err == nil && len(lines) > 0 {
			return lines, nil
		}
	}

	return nil, fmt.Errorf("ytdlp: VTT ファイルが見つかりません (id=%s, langs=%v)", videoID, langs)
}

// VTT タイムスタンプパターン: 00:00:01.234 --> 00:00:03.456
var vttTimestampRe = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})\.(\d{3})\s*-->`)

// parseVTTFile は VTT ファイルをパースして Line スライスを返す。
func parseVTTFile(path string) ([]Line, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []Line
	scanner := bufio.NewScanner(f)
	var currentStart float64
	var currentText strings.Builder

	for scanner.Scan() {
		text := scanner.Text()

		if m := vttTimestampRe.FindStringSubmatch(text); len(m) >= 5 {
			// 前の行を保存
			if currentText.Len() > 0 {
				lines = append(lines, Line{
					Text:  strings.TrimSpace(currentText.String()),
					Start: currentStart,
				})
				currentText.Reset()
			}
			// 新しいタイムスタンプ
			h, _ := strconv.Atoi(m[1])
			min, _ := strconv.Atoi(m[2])
			sec, _ := strconv.Atoi(m[3])
			ms, _ := strconv.Atoi(m[4])
			currentStart = float64(h*3600+min*60+sec) + float64(ms)/1000.0
			continue
		}

		// 空行やヘッダをスキップ
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || trimmed == "WEBVTT" || strings.HasPrefix(trimmed, "Kind:") || strings.HasPrefix(trimmed, "Language:") {
			continue
		}

		// VTT のタグを除去
		trimmed = stripVTTTags(trimmed)
		if trimmed != "" {
			if currentText.Len() > 0 {
				currentText.WriteString(" ")
			}
			currentText.WriteString(trimmed)
		}
	}

	// 最後の行
	if currentText.Len() > 0 {
		lines = append(lines, Line{
			Text:  strings.TrimSpace(currentText.String()),
			Start: currentStart,
		})
	}

	// Duration を計算 (前の行との差分)
	for i := 0; i < len(lines)-1; i++ {
		lines[i].Duration = lines[i+1].Start - lines[i].Start
	}
	if len(lines) > 0 {
		lines[len(lines)-1].Duration = 3.0 // 最後の行はデフォルト 3 秒
	}

	// 重複行を除去 (自動字幕は重複が多い)
	lines = dedup(lines)

	return lines, nil
}

// stripVTTTags は VTT のインラインタグ (<c>, </c>, <00:01.234> 等) を除去する。
var vttTagRe = regexp.MustCompile(`<[^>]+>`)

func stripVTTTags(s string) string {
	return vttTagRe.ReplaceAllString(s, "")
}

// dedup は連続する同一テキストの行を除去する。
func dedup(lines []Line) []Line {
	if len(lines) == 0 {
		return lines
	}
	result := []Line{lines[0]}
	for i := 1; i < len(lines); i++ {
		if lines[i].Text != lines[i-1].Text {
			result = append(result, lines[i])
		}
	}
	return result
}
