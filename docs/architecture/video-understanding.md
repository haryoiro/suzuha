# 動画理解 (Video Understanding)

## 概要

YouTube 等の動画 URL が会話に出た際に、動画の内容を理解して会話に参加できるようにする。

## 設計方針

- **字幕優先**: 最も軽量で情報密度が高い
- **映像は必要時のみ**: 話題に関連するシーンだけフレーム抽出 → VLM で描写
- **ツールとして実装**: LLM が必要と判断したときに呼ぶ
- **URL 自動検知 + メタデータ置換**: Perceive で URL を検知し、メタデータを付加して LLM にツールの存在を伝える
- **振る舞い定義 → 実装差し替え可能**: インターフェースで抽象化し、フォールバック付き

## URL 自動検知 (Perceive)

メッセージ中の動画 URL をパターンマッチし、メタデータで enrich する。
画像の `[IMG]` タグと同じパターン。

```
Before (ユーザーの生メッセージ):
  この動画面白い https://youtube.com/watch?v=xxx

After (Perceive で置換):
  この動画面白い [動画: "超かぐや姫MV" (3:42) | video_watch/video_look で視聴可能] https://youtube.com/watch?v=xxx
```

### 処理フロー

```
Perceive
  ├─ メッセージ中の URL をパターンマッチ
  │   youtube.com/watch, youtu.be, nicovideo.jp, etc.
  │
  ├─ 対応 URL ならメタデータを軽量取得
  │   yt-dlp --dump-json --skip-download (タイトル + 再生時間のみ)
  │   or Go ライブラリでタイトル取得
  │
  └─ メッセージ内の URL を enriched 形式に置換
      [動画: "タイトル" (MM:SS) | video_watch/video_look で視聴可能]
```

- transcript 全体は取らない (メタデータだけなので軽い)
- LLM は置換されたテキストを見て、必要ならツールを呼ぶ
- 不要なら無視する (自動展開ではない)

## ツール設計

### `video_watch` — 動画を視聴する

```
入力:
  url: string       — 動画 URL (YouTube, ニコニコ等)
  lang?: string     — 字幕言語 (デフォルト: "ja", フォールバック: "en")

出力:
  title: string     — 動画タイトル
  duration: string  — 再生時間
  transcript: string — 字幕テキスト (タイムスタンプ付き)

Description:
  動画の字幕を取得して内容を理解する。
  YouTube, ニコニコ動画, Twitch 等の動画 URL に対応。
```

### `video_look` — 動画の特定シーンを見る

```
入力:
  url: string        — 動画 URL
  timestamp: string  — 見たい時点 ("1:23")
  question?: string  — 何を見たいか (VLM へのプロンプト補足)

出力:
  description: string — VLM による画像の描写テキスト

Description:
  動画の特定時点のフレームを切り出して視覚的に確認する。
  video_watch の字幕で気になった箇所を映像で確認する際に使う。
```

## 振る舞い定義 (インターフェース)

```go
package video

// TranscriptLine はタイムスタンプ付きの字幕1行。
type TranscriptLine struct {
    Text     string
    Start    float64 // 秒
    Duration float64 // 秒
}

// VideoInfo は動画のメタデータ。
type VideoInfo struct {
    Title    string
    Duration float64 // 秒
}

// TranscriptFetcher は動画の字幕テキストを取得する。
type TranscriptFetcher interface {
    // Supports はこの fetcher が指定 URL をサポートするか返す。
    Supports(url string) bool
    // FetchTranscript は動画の字幕を取得する。
    // 字幕が取得できない場合は error を返す (次のフォールバックに委譲)。
    FetchTranscript(ctx context.Context, url string, langs []string) (VideoInfo, []TranscriptLine, error)
}

// FrameExtractor は動画の特定時点のフレームを取得する。
type FrameExtractor interface {
    // ExtractFrame は指定時点の JPEG フレームを返す。
    ExtractFrame(ctx context.Context, url string, timestampSec float64) ([]byte, error)
}

// MetadataFetcher は動画のメタデータだけを軽量に取得する (Perceive 用)。
type MetadataFetcher interface {
    Supports(url string) bool
    FetchMetadata(ctx context.Context, url string) (VideoInfo, error)
}
```

## TranscriptFetcher 実装 (フォールバックチェーン)

```
FetchTranscript(url, ["ja", "en"])
    │
    ├─ 1st: GoTranscriptFetcher (Go ライブラリ, YouTube のみ)
    │       youtube-transcript-api-go で YouTube 内部 API から字幕取得
    │       ✓ 速い、外部依存なし
    │       ✗ YouTube のみ、star 18 solo maintainer
    │
    ├─ 2nd: YtDlpTranscriptFetcher (yt-dlp CLI, 800+ サイト対応)
    │       yt-dlp --write-auto-sub --sub-lang ja --skip-download
    │       ✓ 堅牢、多プラットフォーム
    │       ✗ 外部バイナリ依存、やや遅い
    │
    └─ 3rd: (将来) STTTranscriptFetcher
            yt-dlp -x で音声抽出 → STT
            字幕が一切ない動画用。コスト発生。
```

### ChainFetcher

```go
// ChainFetcher は複数の TranscriptFetcher を順に試す。
type ChainFetcher struct {
    fetchers []TranscriptFetcher
    logger   *slog.Logger
}

func (c *ChainFetcher) FetchTranscript(ctx context.Context, url string, langs []string) (VideoInfo, []TranscriptLine, error) {
    var lastErr error
    for _, f := range c.fetchers {
        if !f.Supports(url) {
            continue
        }
        info, lines, err := f.FetchTranscript(ctx, url, langs)
        if err == nil {
            return info, lines, nil
        }
        lastErr = err
        c.logger.Debug("transcript fetcher failed, trying next", "error", err)
    }
    return VideoInfo{}, nil, fmt.Errorf("all fetchers failed: %w", lastErr)
}
```

## FrameExtractor 実装

yt-dlp + ffmpeg が必須。

```go
// YtDlpFrameExtractor は yt-dlp + ffmpeg で動画フレームを切り出す。
type YtDlpFrameExtractor struct{}

func (e *YtDlpFrameExtractor) ExtractFrame(ctx context.Context, url string, timestampSec float64) ([]byte, error) {
    // 1. yt-dlp -g --format "best[height<=720]" URL → stream URL
    // 2. ffmpeg -ss {timestamp} -i {stream_url} -frames:v 1 -f image2 pipe:1
    // 3. stdout から JPEG バイトを返す
}
```

## video_look ツールのフロー

```
LLM: video_look(url="...", timestamp="1:30", question="何が映っている？")
  │
  ├─ FrameExtractor.ExtractFrame(url, 90.0) → JPEG bytes
  │
  ├─ JPEG を base64 data URI に変換
  │
  ├─ llm.Client.WithCapability("conversation", "vision")
  │   ├─ inline=true  → 画像を content part として LLM に直接渡す
  │   └─ inline=false → DescribeImage() で VLM に描写させる
  │
  └─ テキスト描写を tool result として返す
```

## transcript のテキスト形式

```
[動画] タイトル: ○○○
[動画] 長さ: 12:34

[00:00] こんにちは、今日は○○について話します
[00:15] まず最初に...
[01:23] ここが重要なポイントで...
...
```

タイムスタンプ付きにすることで video_look の timestamp 指定が自然にできる。

## 使用例

### 例 1: URL が貼られた → 聞かれたら見る

```
[user] この動画面白い https://youtube.com/watch?v=xxx
  ↓ Perceive で置換
[user] この動画面白い [動画: "超かぐや姫MV" (3:42) | video_watch/video_look] https://youtube.com/watch?v=xxx

LLM: (skip_response — 見てと言われていない)

[user] 見てみて
LLM: → video_watch(url="https://youtube.com/watch?v=xxx")
     → 「mili の超かぐや姫の MV だね。○○な内容で...」
```

### 例 2: 映像が重要な場面

```
[user] 1:30 あたりのシーン見た？
LLM: → video_look(url="...", timestamp="1:30", question="何が映っている？")
     → 「猫が箱に入ろうとして失敗してるシーンだね」
```

### 例 3: Go ライブラリが壊れた場合

```
LLM: → video_watch(url="...")
     → GoTranscriptFetcher: error (YouTube API 変更)
     → YtDlpTranscriptFetcher: success (フォールバック)
     → 「○○の動画だね」
```

## 依存関係

```
Perceive (URL 検知 + メタデータ置換)
    └── MetadataFetcher
        └── yt-dlp --dump-json (or Go ライブラリ)

video_watch
    └── ChainFetcher
        ├── GoTranscriptFetcher → go get (Docker 変更不要)
        └── YtDlpTranscriptFetcher → yt-dlp binary

video_look
    └── YtDlpFrameExtractor → yt-dlp + ffmpeg

共通
    └── llm.Client.WithCapability() (VLM 解決)
```

## コンテナへの追加

```dockerfile
RUN apt-get update && apt-get install -y ffmpeg python3-pip \
    && pip3 install --break-system-packages yt-dlp
```

GoTranscriptFetcher のみなら Docker 変更不要。
video_look / yt-dlp フォールバックを使う場合は上記が必要。

## パッケージ構成

```
internal/feature/video/
├── feature.go              — scheduler.Feature (ツール登録のみ)
├── watch_tool.go           — video_watch ツール実装
├── look_tool.go            — video_look ツール実装
├── detect.go               — URL パターンマッチ (Perceive 用)
├── metadata.go             — MetadataFetcher (Perceive 用メタデータ取得)
├── fetcher.go              — TranscriptFetcher interface + ChainFetcher
├── fetcher_go.go           — GoTranscriptFetcher (Go ライブラリ)
├── fetcher_ytdlp.go        — YtDlpTranscriptFetcher (yt-dlp CLI)
├── extractor.go            — FrameExtractor interface
└── extractor_ytdlp.go      — YtDlpFrameExtractor (yt-dlp + ffmpeg)
```

## 制約・注意

- transcript が長い場合は truncate (LLM コンテキストに収まるように)
- yt-dlp はレート制限やブロックされる可能性がある
- 要ログイン動画は不可
- 一時ファイルは処理後削除

## 将来の拡張

- **STTTranscriptFetcher**: 字幕なし動画用 (yt-dlp -x + STT)
- **自動展開モード**: チャンネル設定で URL 検知時に自動で transcript を取得
- **動画メモリ**: transcript をメモリに保存し後から参照可能に
- **ライブ配信**: HLS/DASH ストリームの定期サンプリング
