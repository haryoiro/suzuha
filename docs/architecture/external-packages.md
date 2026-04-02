# external パッケージ設計

## 概要

外部サービスの SDK/クライアントを `external/` に集約する。
ビジネスロジックなし、インターフェース定義 + 実装のみ。

## 動機

現状、外部サービスのクライアントが feature パッケージに散在している:

| クライアント | 現在の場所 | 問題 |
|---|---|---|
| STT (Deepgram, Whisper) | `internal/voice/` | voice 以外 (video) から使えない |
| TTS (Voicevox, SBV2) | `internal/voice/` | voice に閉じ込められている |
| Web 検索 (SearXNG) | `internal/websearch/` | OK だが命名が不統一 |
| Embedding (Gemini, OpenAI) | `internal/embedding/` | OK だが位置が不統一 |
| YOLO (物体検出) | `internal/device/detect.go` | device に閉じ込められている |

これらは **feature をまたいで再利用される** SDK。
`external/` に切り出すことで、どの feature からも import 可能にする。

## 目標構成

```
external/
├── stt/                      — 音声認識
│   ├── stt.go                — STT interface + ChainSTT
│   ├── deepgram.go           — Deepgram 実装
│   ├── whisper.go            — Whisper.cpp 実装
│   └── wav.go                — PCM→WAV 変換ユーティリティ
│
├── tts/                      — 音声合成
│   ├── tts.go                — TTS interface
│   ├── voicevox.go           — Voicevox 実装
│   └── sbv2.go               — StyleBertVITS2 実装
│
├── embedding/                — ベクトル埋め込み
│   ├── embedder.go           — Embedder interface + Part 型
│   ├── gemini.go             — Gemini multimodal 実装
│   └── text_only.go          — OpenAI 等テキスト専用 実装
│
├── search/                   — Web 検索
│   └── searxng.go            — SearXNG クライアント
│
├── transcript/               — 動画字幕取得
│   ├── transcript.go         — TranscriptFetcher interface + ChainFetcher
│   ├── youtube.go            — Go ライブラリ (YouTube 内部 API)
│   └── ytdlp.go              — yt-dlp CLI ラッパー
│
└── detect/                   — 物体検出
    └── yolo.go               — YOLO クライアント
```

## 設計原則

### 1. インターフェース + 実装

各パッケージは 1 つのインターフェースを定義し、複数の実装を提供する。

```go
// external/stt/stt.go
package stt

type STT interface {
    Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}
```

### 2. ビジネスロジックなし

- 外部 API の呼び出しとレスポンスの変換のみ
- リトライ、フォールバックは Chain パターンで対応
- ユースケース固有のロジック (VAD、ハルシネーション除去等) は呼び出し側に置く

### 3. 設定は構造体で注入

```go
// external/stt/deepgram.go
type DeepgramConfig struct {
    APIKey string
    Model  string // default: "nova-3"
}

func NewDeepgram(cfg DeepgramConfig) *DeepgramClient
```

config.yaml のパースや DI 登録は呼び出し側 (providers.go 等) の責務。

### 4. Chain パターンは共通

STT, TranscriptFetcher 等の複数プロバイダをフォールバックで試すパターンは各パッケージに Chain 実装を持つ。

## 移行計画

### Phase 1: STT/TTS (video feature の前提)

```
internal/voice/stt.go           → external/stt/stt.go
internal/voice/stt_deepgram.go  → external/stt/deepgram.go
internal/voice/stt_whisper.go   → external/stt/whisper.go
internal/voice/stt_wav.go       → external/stt/wav.go
internal/voice/tts.go           → external/tts/tts.go
internal/voice/tts_voicevox.go  → external/tts/voicevox.go
internal/voice/tts_sbv2.go      → external/tts/sbv2.go
```

`internal/voice/` は `external/stt` と `external/tts` を import する薄いパイプラインになる。

### Phase 2: Embedding

```
internal/embedding/embedder.go   → external/embedding/embedder.go
internal/embedding/gemini.go     → external/embedding/gemini.go
internal/embedding/text_only.go  → external/embedding/text_only.go
```

### Phase 3: Search + Detect

```
internal/websearch/  → external/search/
internal/device/detect.go → external/detect/yolo.go
```

### Phase 4: Transcript (video feature 実装時)

```
新規: external/transcript/
```

## 利用側の変化

```go
// Before: internal/voice/pipeline.go
import "github.com/haryoiro/suzuha/internal/voice"
p.stt.Transcribe(ctx, pcm, 48000)  // voice パッケージ内の STT

// After: internal/voice/pipeline.go
import "github.com/haryoiro/suzuha/external/stt"
p.stt.Transcribe(ctx, pcm, 48000)  // external パッケージの STT

// After: internal/feature/video/fetcher_stt.go
import "github.com/haryoiro/suzuha/external/stt"
f.stt.Transcribe(ctx, pcm, sampleRate)  // 同じインターフェースを共有
```

## 命名規則

- パッケージ名 = 機能名 (`stt`, `tts`, `search`, `embedding`)
- インターフェース名 = パッケージ名と同じか、その機能を表す名詞 (`stt.STT`, `embedding.Embedder`)
- 実装名 = プロバイダ名 (`stt.DeepgramClient`, `tts.VoicevoxClient`)
- config = `{Provider}Config` (`stt.DeepgramConfig`)

## 注意

- `external/` は外部サービスのクライアントであり、suzuha 固有のドメインロジックを含まない
- テストは各実装ごとにモックサーバーで書く
- API キー等のシークレットは config/env 経由で注入、external パッケージ内にハードコードしない
