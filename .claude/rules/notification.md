---
applyTo: "**"
paths: "internal/notification/**,internal/chat/**,internal/scheduler/**"
---

# 通知パイプライン

## 概要

Consolidator プロセスから Agent プロセスを経由して Discord にメッセージを送る仕組み。
スケジューラのタスク（RSS、Topics 等）が Discord チャンネルにメッセージを投稿する際に使う。

```
CronTask ─── NotifyFunc ──→ gRPC ──→ Agent NotificationServer ──→ chat.Interface ──→ Discord
         └── ReplyNotifier ─┘
```

## 二つの送信経路

### NotifyFunc（基本経路）

`func(ctx, channelID, content, source string) error`

- 戻り値はエラーのみ。メッセージ ID は返さない
- Quiet Hours ラッパーで深夜帯の通知を抑制可能
- RSS タスクなどメッセージ ID が不要なタスクが使う

### ReplyNotifier（拡張経路）

`Notify(ctx, channelID, content, source) → (messageID, error)`
`Reply(ctx, channelID, content, replyToID, source) → (messageID, error)`

- メッセージ ID を返す → 後から Reply で参照できる
- Topics タスクが使う: 話題投稿の ID を記憶に保存し、次回 REPLY アクション時にリプライ先として指定

両方とも内部では同じ gRPC `SendMessage` を呼ぶ。違いは `reply_to_message_id` フィールドの有無とレスポンスの `message_id` を呼び出し元に返すかどうか。

## gRPC プロトコル

`proto/notification/v1/notification.proto`

```protobuf
service NotificationService {
  rpc SendMessage(SendMessageRequest) returns (SendMessageResponse);
}

message SendMessageRequest {
  string channel_id          = 1;
  string content             = 2;
  string source              = 3;  // "rss", "topics" 等
  string reply_to_message_id = 4;  // 空ならリプライなし
}

message SendMessageResponse {
  bool   ok         = 1;
  string error      = 2;
  string message_id = 3;  // 送信されたメッセージの ID
}
```

## Agent 側サーバーのルーティング

`notification/server.go` の `SendMessage` は chat.Interface の能力に応じてディスパッチする。

```
reply_to_message_id あり?
  ├── Yes → chat が Replier を実装？
  │     ├── Yes → SendReply(channel, text, replyToID)
  │     └── No  → Send(channel, text)  // フォールバック
  └── No  → chat が IDSender を実装？
        ├── Yes → SendWithID(channel, text)  // ID を返せる
        └── No  → Send(channel, text)
```

## Optional Interface パターン

`chat.Interface` 本体を変更せず、プラットフォーム固有の機能を追加するために Go のオプショナルインターフェースを使う（`io.ReaderAt` 等と同じパターン）。

```go
// chat/chat.go
type Interface interface {
    Run(ctx) error
    Send(ctx, channel, text string) error
}

type Replier interface {
    SendReply(ctx, channel, text, replyToID string) (messageID string, err error)
}

type IDSender interface {
    SendWithID(ctx, channel, text string) (messageID string, err error)
}
```

Discord 実装はこの 3 つすべてを満たす。CLI 実装は Interface のみ。型アサーション `chat.(Replier)` で能力を判定する。

## Quiet Hours

`notification/quiet.go` — NotifyFunc をラップして深夜帯の通知を抑制するデコレータ。

```go
notifier = WithQuietHours(notifier, QuietHoursConfig{
    Start:    "23:00",
    End:      "08:00",
    Location: loc,
}, logger)
```

- `Start` > `End`（日跨ぎ）にも対応。分単位で比較
- 設定は `config.yaml` の `consolidator.scheduler.quiet_hours`
- **NotifyFunc のみに適用**。ReplyNotifier には適用されない（必要に応じて別途ラップ）

## ファイル構成

```
internal/notification/
  client.go          # NotifyFunc, NewGRPCNotifier
  reply_notifier.go  # ReplyNotifier（ID 付き送信 + リプライ）
  server.go          # gRPC サーバー（Agent 側、chat.Interface に委譲）
  quiet.go           # WithQuietHours デコレータ
  server_test.go     # サーバーのルーティングテスト
```
