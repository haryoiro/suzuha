# スケジューラ（Cron）システム

## 概要

suzuha-consolidator プロセス内で動作する定期実行ジョブ機構。
`tool.Tool` と同じプラグインパターンで、各ジョブを **CronTask** インターフェースとして実装する。

Agent 側の `trigger/` パッケージ（EventBus にイベントを発行する仕組み）とは独立しており、Consolidator 側で自律的にジョブを実行する。

## アーキテクチャ

```
suzuha-consolidator
  ├── Compact (既存: Agent からの gRPC 呼び出しで記憶圧縮)
  └── Scheduler
       ├── TaskRegistry   ← CronTask の登録簿
       └── cron.Cron      ← robfig/cron/v3 によるスケジューリング
            │
            │ tick ごとに
            ▼
       CronTask.Execute(ctx, CronContext, config)
                              │
                              ├── LLM Client      (LLM 呼び出し)
                              ├── Memory Store    (長期記憶の読み書き)
                              ├── NotifyFunc      (Discord 通知)
                              ├── ReplyNotifier   (ID 付き通知 + リプライ)
                              ├── *sql.DB         (タスク固有テーブル)
                              └── Logger
```

### 通知パイプライン

CronTask が Discord にメッセージを送る方法は 2 つ。詳細は `notification.md` を参照。

**NotifyFunc** — シンプルな送信。Quiet Hours ラッパー付き。

```
CronTask → Notifier(channelID, text, source) → gRPC → Agent → Discord
```

**ReplyNotifier** — メッセージ ID 取得 + リプライ対応。

```
CronTask → ReplyNotifier.Notify(channelID, text, source) → (messageID, error)
CronTask → ReplyNotifier.Reply(channelID, text, replyToID, source) → (messageID, error)
```

## 組み込みタスク

### RSS (`rss`)

RSS/Atom フィードを監視し、ユーザーの興味に合う新着記事を Discord に通知する。

**処理フロー:**

1. `rss_feeds` テーブルから有効なフィードを取得
2. 各フィードを HTTP で取得し、RSS 2.0 / Atom を自動判定してパース
3. 未読の記事を `rss_items` に保存し、長期記憶にも Embedding 付きで保存
4. ユーザーの興味プロファイル（user 型メモリ）を収集
5. **Phase A**: ベクトル類似度で粗いフィルタ（`vector_threshold`）
6. **Phase B**: LLM バッチスコアリングでスコアが閾値以上の記事を選別（`notify_threshold`）
7. LLM で suzuha の口調の通知メッセージを生成し Discord に送信

**設定例:**

```yaml
consolidator:
  scheduler:
    jobs:
      - name: "rss-check"
        task: "rss"
        cron: "*/30 * * * *"
        config:
          vector_threshold: 0.3
          notify_threshold: 0.6
          max_articles_per_notify: 5
```

フィードの登録・管理は `rss_feeds` テーブルを直接操作する（将来の管理画面対応予定）。

### Topics (`topics`)

定期的にチャンネルに話題を投稿する。コンテキスト（最近の会話、過去の話題の反応状況、時間帯）を考慮して LLM が生成する。

**処理フロー:**

1. **バックオフ判定**: 前回の話題に反応がなければ `consecutiveNoResponse` を増やし、次回以降スキップ（最大 `maxBackoff=5` 回）
2. バックオフ中ならスキップして終了
3. opt-in ユーザー（`metadata.mention_opt_in = 1`）のリストと記憶を取得
4. 最近のチャンネル記憶 + 過去の話題投稿を取得
5. **アクション決定**: LLM に NEW / REPLY / SUPPLEMENT を選ばせる
   - `NEW`: 新しい話題を投稿
   - `REPLY`: 過去の話題にリプライして会話を続ける
   - `SUPPLEMENT`: 過去の話題に「ちなみに〜」で補足
6. LLM でメッセージ生成（時間帯ヒント、反応履歴、ユーザーコンテキスト付き）
7. 送信（REPLY/SUPPLEMENT は `ReplyNotifier.Reply` でスレッド返信）
8. 長期記憶に保存（message_id 付き → 次回の REPLY 対象に）

**バックオフの仕組み:**

```
反応あり → consecutiveNoResponse = 0, skipCounter = 0
反応なし → consecutiveNoResponse++, skipCounter = min(consecutiveNoResponse, 5)
```

`channel_activity` テーブルの `last_user_message_at` で反応有無を判定。

**設定例:**

```yaml
consolidator:
  scheduler:
    jobs:
      - name: "daily-topic"
        task: "topics"
        cron: "0 */2 * * *"
        config:
          channel_id: "123456789"
          topics:
            - "プログラミング"
            - "最近のニュース"
            - "ゲーム"
          prompt_dir: "prompts/"
          timezone: "Asia/Tokyo"
```

## 設定

`config.yaml` の `consolidator` セクション:

```yaml
consolidator:
  address: "localhost:50051"
  agent_notify: "localhost:50052"
  scheduler:
    enabled: true
    timezone: "Asia/Tokyo"         # IANA タイムゾーン。cron 式の評価に使用
    quiet_hours:
      enabled: true
      start: "23:00"               # この時間帯は NotifyFunc 経由の通知を抑制
      end: "08:00"
    jobs:
      - name: "rss-check"
        task: "rss"
        cron: "*/30 * * * *"
        config: { ... }
      - name: "daily-topic"
        task: "topics"
        cron: "0 */2 * * *"
        config: { ... }
```

### cron 式の書式

標準の 5 フィールド cron 式に加え、`robfig/cron/v3` の拡張構文が使える。

| 書式 | 意味 |
|------|------|
| `*/30 * * * *` | 30 分ごと |
| `0 9 * * *` | 毎日 9:00 |
| `0 9 * * 1-5` | 平日の 9:00 |
| `@every 10s` | 10 秒ごと |
| `@every 1h` | 1 時間ごと |
| `@daily` | 毎日 0:00 |
| `@hourly` | 毎時 0 分 |

### Quiet Hours

`quiet_hours` を有効にすると、指定した時間帯の通知を抑制する。
日跨ぎ（例: `23:00`〜`08:00`）にも対応。詳細は `notification.md` 参照。

## CronContext で使えるサービス

| フィールド | 型 | 用途 |
|-----------|-----|------|
| `LLM` | `*llm.Client` | LLM 呼び出し (Complete, Embed) |
| `Memory` | `memory.Store` | 長期記憶の検索・保存 |
| `Notifier` | `NotifyFunc` | Discord チャンネルへの通知送信 |
| `ReplyNotifier` | `*notification.ReplyNotifier` | ID 付き通知 + リプライ送信 |
| `DB` | `*sql.DB` | SQLite への直接クエリ（タスク固有テーブル用） |
| `Logger` | `*slog.Logger` | 構造化ログ |

## CronTask の実装方法

### 1. CronTask インターフェースを実装

```go
// internal/scheduler/tasks/example.go
package tasks

type ExampleTask struct{}

func (t *ExampleTask) Name() string        { return "example" }
func (t *ExampleTask) Description() string { return "サンプルタスク" }

func (t *ExampleTask) Setup(ctx context.Context, cc *scheduler.CronContext) error {
    return nil
}

func (t *ExampleTask) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
    var conf struct {
        ChannelID string `json:"channel_id"`
    }
    json.Unmarshal(cfg, &conf)
    return cc.Notifier(ctx, conf.ChannelID, "Hello!", "example")
}
```

### 2. レジストリに登録

`cmd/suzuha-consolidator/main.go`:

```go
taskRegistry := scheduler.NewRegistry()
taskRegistry.Register(&tasks.ExampleTask{})
```

### 3. config.yaml にジョブ定義

```yaml
consolidator:
  scheduler:
    enabled: true
    jobs:
      - name: "example-notify"
        task: "example"
        cron: "@every 1h"
        config:
          channel_id: "123456789"
```

## ファイル構成

```
internal/
  scheduler/
    task.go          # CronTask interface, CronContext
    registry.go      # TaskRegistry
    scheduler.go     # Scheduler (robfig/cron/v3 ラッパー)
    tasks/
      rss.go         # RSSTask
      rss_store.go   # FeedStore (rss_feeds, rss_items テーブル操作)
      topics.go      # TopicsTask

  notification/
    client.go          # NotifyFunc, NewGRPCNotifier
    reply_notifier.go  # ReplyNotifier
    server.go          # gRPC NotificationServer (Agent 側)
    quiet.go           # WithQuietHours デコレータ

proto/
  notification/v1/
    notification.proto
```
