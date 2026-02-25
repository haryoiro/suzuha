---
applyTo: "**"
paths: "internal/notification/**,internal/chat/**,internal/scheduler/**"
---

# 通知パイプライン

## 概要

Consolidator プロセスから Agent プロセスを経由して Discord にメッセージを送る仕組み。
スケジューラのタスク（RSS、Topics 等）が Discord チャンネルにメッセージを投稿する際に使う。

```
CronTask ─── Notifier.Send/Reply ──→ gRPC ──→ Agent NotificationServer ──→ chat.Interface ──→ Discord
```

## 統一 Notifier インターフェース

```go
// notification/notifier.go
type SendResult struct {
    MessageID string
}

type Notifier interface {
    Send(ctx, channelID, content, source string) (SendResult, error)
    Reply(ctx, channelID, content, replyToID, source string) (SendResult, error)
}
```

- `Send`: 通常のメッセージ送信。メッセージ ID を返す
- `Reply`: 既存メッセージへのリプライ。プラットフォームが非対応なら通常送信にフォールバック
- 全タスクがこの単一インターフェースを使う（RSS は Send、Topics は Send + Reply）

## Middleware パターン

```go
// notification/middleware.go
type Middleware func(Notifier) Notifier

func Chain(mws ...Middleware) Middleware
func WithQuietHours(cfg QuietHoursConfig, logger *slog.Logger) Middleware
```

Middleware は `Notifier` をラップして追加の振る舞いを付与する。
`Chain(a, b)(notifier)` = `a(b(notifier))`（最初が最外層）。

### Quiet Hours

```go
notifier = WithQuietHours(QuietHoursConfig{
    Start:    "23:00",
    End:      "08:00",
    Location: loc,
}, logger)(notifier)
```

- Send と Reply の両方を抑制（旧実装では NotifyFunc のみだった）
- `Start` > `End`（日跨ぎ）にも対応。分単位で比較
- 設定は `config.yaml` の `consolidator.scheduler.quiet_hours`

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

## ファイル構成

```
internal/notification/
  notifier.go        # Notifier インターフェース, SendResult, NopNotifier
  grpc_notifier.go   # GRPCNotifier（gRPC 経由の Notifier 実装）
  middleware.go       # Middleware, Chain, WithQuietHours
  server.go          # gRPC サーバー（Agent 側、chat.Interface に委譲）
  server_test.go     # サーバーのルーティングテスト
```
