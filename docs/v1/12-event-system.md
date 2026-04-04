# イベントシステム

## Event Bus

**パッケージ:** `internal/event/`

シンプルな Pub/Sub パターン。全コンポーネントがイベントバスを介して通信する。

```go
type Bus struct {
    subscribers []chan Event
}

func (b *Bus) Subscribe() <-chan Event  // チャンネル取得
func (b *Bus) Publish(evt Event)        // イベント発行
```

## イベント構造

```go
type Event struct {
    Source  string   // "discord", "cli", "internal", "device"
    Type   string   // "message", "self_prompt", etc.
    Payload any     // MessagePayload 等
}
```

### MessagePayload

```go
type MessagePayload struct {
    Content     string
    Channel     string
    ChannelName string
    UserID      string
    UserName    string
    ImageURLs   []string
    IsMention   bool
    IsDM        bool
    IsBot       bool
    IsVoice     bool
    GuildID     string
    GuildName   string
    MessageID   string
}
```

## イベントの発行元

| 発行元 | Source | Type | 説明 |
|--------|--------|------|------|
| Discord テキスト | `"discord"` | `"message"` | テキストチャンネルのメッセージ |
| Discord VC | `"discord"` | `"message"` | 音声チャンネルの文字起こし（IsMention=true） |
| CLI | `"cli"` | `"message"` | CLI 入力 |
| Topics タスク | (internal) | `"self_prompt"` | 独り言セルフプロンプト |
| Device (ESP32) | `"device"` | `"message"` | 物理デバイスからの入力 |
| Voice Stream Preview | `"discord"` | `"message"` | 画面共有プレビュー画像 |

## Chat Interface

**パッケージ:** `internal/chat/`

ライフサイクル (Run) と送信 (Send) が分離されている:

```go
// Sender はテキスト送信の基本インターフェース。
// Session や Notifier が使う。
type Sender interface {
    Send(ctx context.Context, channel, text string) error
}

// Interface はライフサイクル + 送信の複合インターフェース (レガシー)。
// 新規コードでは gateway.Source (ライフサイクル) と chat.Sender (送信) を個別に使う。
type Interface interface {
    Sender
    Run(ctx context.Context) error
}

type VoiceSpeaker interface {
    SpeakText(ctx context.Context, guildID, text string) error
    IsConnected(guildID string) bool
}
```

## Gateway

**パッケージ:** `internal/gateway/`

全アダプタのライフサイクルを管理する Hub。各アダプタは `gateway.Source` を実装する:

```go
type Source interface {
    Name() string
    Run(ctx context.Context) error
}
```

Gateway は errgroup で全 Source を起動し、ヘルス状態を追跡する。
`GET /internal/gateway/status` で全ソースの状態を JSON で取得可能。

### 実装

- **Discord** (`internal/adapter/discord/`): discordgo ベース。`gateway.Source` + `chat.Sender` を実装
- **CLI** (`internal/adapter/cli/`): stdin/stdout ベース。`gateway.Source` を実装。開発用
- **Device** (`internal/adapter/device/`): ESP32 WebSocket Hub。`device.Source` でラップ

## イベントの Agent 内での処理

```
Event Bus
    │
    │ Subscribe()
    ▼
Agent.Run() → sourceKeyForEvent() で振り分け
    │
    ├─ Discord Worker ─── ドレイン (3s) → batch
    ├─ Device Worker ──── ドレイン (2s) → batch
    ├─ Web Worker ─────── ドレイン (2s) → batch
    └─ CLI Worker ─────── ドレイン (1s) → batch
         │
         ▼ (各 Worker が独立に処理)
    handleBatchWith(sourceKey, batch)
         │
         ├─ Perceive: Event → llm.Message 変換
         │     source="internal" → role="system"
         │     source="discord" → role="user"
         │     type="self_prompt" → エフェメラル注入
         │
         ├─ Think: ディレクティブ決定
         │     DirectlyAddressed (mention/DM/CLI/voice/self_prompt) → [RESPOND]
         │     会話状態分析 → [RESPOND] or [LISTEN]
         │
         ├─ Act: LLM 呼び出し + ツール実行
         │
         ├─ Respond: Session がルーティング
         │     DiscordSession → chat.Send() or voice.SpeakText()
         │     DeviceSession  → hub.SpeakText() (TTS → ESP32)
         │     WebSession     → hub.SpeakTextTo("web")
         │     CLISession     → stdout
         │
         └─ Reflect: ログ + コンテキスト永続化
```

Worker の数は動的。登録された `SourceRegistration` の数だけ Worker が起動する。
