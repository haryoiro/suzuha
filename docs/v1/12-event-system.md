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

```go
type Interface interface {
    Run(ctx context.Context) error
    Send(ctx context.Context, channel, text string) error
}

type VoiceSpeaker interface {
    SpeakText(ctx context.Context, guildID, text string) error
    IsConnected(guildID string) bool
}
```

### 実装

- **Discord** (`internal/adapter/discord/`): discordgo ベース。メッセージ受信 → イベントバス発行、メッセージ送信。OnReady コールバックで Discord 固有ツールを登録
- **CLI** (`internal/adapter/cli/`): stdin/stdout ベース。開発用

## イベントの Agent 内での処理

```
Event Bus
    │
    │ Subscribe()
    ▼
Agent.Run() → ドレイン → batch
    │
    ▼
handleBatch(batch)
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
    │     応答ルーティング:
    │       source="device" → deviceSpeaker.SpeakText()
    │       VC 接続中 → voiceSpeaker.SpeakText()
    │       それ以外 → chat.Send()
    │
    └─ Reflect: ログ + 永続化
```
