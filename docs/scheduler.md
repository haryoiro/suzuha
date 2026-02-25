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
                              ├── Notifier        (統一通知: Send + Reply)
                              ├── *sql.DB         (タスク固有テーブル)
                              ├── Logger
                              ├── Timezone        (スケジューラレベル TZ)
                              └── SystemPrompt    (IDENTITY.md + SOUL.md)
```

### 通知パイプライン

CronTask が Discord にメッセージを送るには統一 `Notifier` インターフェースを使う。詳細は `notification.md` を参照。

```
CronTask → cc.Notifier.Send(ctx, channelID, text, source) → (SendResult, error)
CronTask → cc.Notifier.Reply(ctx, channelID, text, replyToID, source) → (SendResult, error)
```

Quiet Hours Middleware は Send と Reply の両方を抑制する。

## Feature パターン

各機能（RSS、Topics 等）は `scheduler.Feature` を実装し、ツール・タスク・DB セットアップを1つのパッケージにまとめる。

```go
// scheduler/feature.go
type Feature interface {
    Name() string
    Setup(ctx context.Context, db *sql.DB) error
    Tools() []tool.Tool
    Tasks() []CronTask
}
```

main.go で Feature 配列をループして登録:

```go
features := []scheduler.Feature{rss.New(db, mem), topics.New()}
for _, f := range features {
    f.Setup(ctx, db)
    for _, t := range f.Tasks() { taskRegistry.Register(t) }
}
```

## 組み込みタスク

### RSS (`internal/rss/`)

RSS/Atom フィードを監視し、ユーザーの興味に合う新着記事を Discord に通知する。

**処理フロー:**

1. `rss_feeds` テーブルから有効なフィードを取得
2. 各フィードを HTTP で取得し、RSS 2.0 / Atom を自動判定してパース
3. 未読の記事を `rss_items` に保存し、長期記憶にも Embedding 付きで保存
4. ユーザーの興味プロファイル（user 型メモリ）を収集
5. **Phase A**: ベクトル類似度で粗いフィルタ（`vector_threshold`）
6. **Phase B**: LLM バッチスコアリングでスコアが閾値以上の記事を選別（`notify_threshold`）
7. LLM で suzuha の口調の通知メッセージを生成し `cc.Notifier.Send` で Discord に送信

**Agent 側ツール:**

- `rss_subscribe` — フィード登録
- `rss_unsubscribe` — フィード削除
- `rss_list` — フィード一覧
- `rss_preference` — ユーザーの通知設定

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

### Topics (`internal/topics/`)

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
7. 送信（REPLY/SUPPLEMENT は `cc.Notifier.Reply` でスレッド返信、NEW は `cc.Notifier.Send`）
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
```

`timezone` と `prompt_dir` はタスク config ではなく、`CronContext.Timezone` と `CronContext.SystemPrompt` から取得する。

## 設定

`config.yaml` の `consolidator` セクション:

```yaml
consolidator:
  address: "localhost:50051"
  agent_notify: "localhost:50052"
  scheduler:
    enabled: true
    timezone: "Asia/Tokyo"         # IANA タイムゾーン。cron 式の評価 + CronContext.Timezone に使用
    quiet_hours:
      enabled: true
      start: "23:00"               # この時間帯は全通知（Send + Reply）を抑制
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

`quiet_hours` を有効にすると、指定した時間帯の通知を抑制する（Send と Reply の両方）。
日跨ぎ（例: `23:00`〜`08:00`）にも対応。詳細は `notification.md` 参照。

## CronContext で使えるサービス

| フィールド | 型 | 用途 |
|-----------|-----|------|
| `LLM` | `*llm.Client` | LLM 呼び出し (Complete, Embed) |
| `Memory` | `memory.Store` | 長期記憶の検索・保存 |
| `Notifier` | `notification.Notifier` | 統一通知（Send + Reply → SendResult） |
| `DB` | `*sql.DB` | SQLite への直接クエリ（タスク固有テーブル用） |
| `Logger` | `*slog.Logger` | 構造化ログ |
| `Timezone` | `*time.Location` | スケジューラレベルのタイムゾーン |
| `SystemPrompt` | `string` | IDENTITY.md + SOUL.md から読み込み済み |

## 新機能の追加方法

### 1. Feature パッケージを作成

```go
// internal/myfeature/feature.go
package myfeature

type Feature struct{}

func New() *Feature { return &Feature{} }
func (f *Feature) Name() string { return "myfeature" }
func (f *Feature) Setup(ctx context.Context, db *sql.DB) error { return nil }
func (f *Feature) Tools() []tool.Tool { return nil }
func (f *Feature) Tasks() []scheduler.CronTask { return []scheduler.CronTask{&Task{}} }
```

### 2. CronTask を実装

```go
// internal/myfeature/task.go
package myfeature

type Task struct{}

func (t *Task) Name() string        { return "myfeature" }
func (t *Task) Description() string { return "サンプルタスク" }
func (t *Task) Setup(_ context.Context, _ *scheduler.CronContext) error { return nil }

func (t *Task) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
    var conf struct {
        ChannelID string `json:"channel_id"`
    }
    json.Unmarshal(cfg, &conf)
    _, err := cc.Notifier.Send(ctx, conf.ChannelID, "Hello!", "myfeature")
    return err
}
```

### 3. main.go の features 配列に追加

```go
features := []scheduler.Feature{
    rss.New(db, mem),
    topics.New(),
    myfeature.New(),  // ← 追加
}
```

### 4. config.yaml にジョブ定義

```yaml
consolidator:
  scheduler:
    enabled: true
    jobs:
      - name: "myfeature-notify"
        task: "myfeature"
        cron: "@every 1h"
        config:
          channel_id: "123456789"
```

## ファイル構成

```
internal/
  scheduler/
    feature.go       # Feature インターフェース
    task.go          # CronTask interface, CronContext
    registry.go      # TaskRegistry
    scheduler.go     # Scheduler (robfig/cron/v3 ラッパー)

  rss/               # Feature: RSS フィード監視
    feature.go       # Feature 実装
    task.go          # RSSTask (フィード取得 + スコアリング + 通知)
    tools.go         # Agent ツール (subscribe, unsubscribe, list, preference)
    store.go         # FeedStore (rss_feeds, rss_items テーブル操作)

  topics/            # Feature: 話題提供
    feature.go       # Feature 実装
    task.go          # TopicsTask (コンテキスト対応話題生成)

  notification/
    notifier.go      # Notifier インターフェース, SendResult, NopNotifier
    grpc_notifier.go # GRPCNotifier
    middleware.go    # Middleware, Chain, WithQuietHours
    server.go        # gRPC NotificationServer (Agent 側)

proto/
  notification/v1/
    notification.proto
```
