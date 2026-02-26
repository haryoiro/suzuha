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

### Topics / 独り言 (`internal/topics/`)

定期的にチャンネルに独り言をつぶやく。時間帯・最近の会話・過去のつぶやきを参考に、LLM が1〜2文の短い独り言を生成する。

**設計思想:** 「話題提供」ではなく「独り言」。誰かに向けた発言ではなく、ふと思ったことをぽろっとつぶやく。質問しない、メンションしない、具体的すぎる事柄を捏造しない。

**退屈システム:**

バックオフ（反応なし→頻度を下げる）を廃止し、退屈度ベースの投稿判定に置き換えた。

- 退屈度 = 最後のユーザーメッセージからの経過時間 × `boredomRate`（1時間あたり 8）
- 退屈度 < 20（≒2.5時間以内） → 投稿しない
- 退屈度 20–100 → 退屈度に比例した確率で投稿（最大 85%）
- 退屈度が高いほど「暇そうな」トーンの独り言になる（LLM プロンプトに退屈度を注入）
- `channel_activity.last_user_message_at` をインタラクション時刻として使用

**処理フロー:**

1. `channel_activity` から最後のインタラクション時刻を取得
2. 退屈度を計算（経過時間 × rate、上限 100）
3. 退屈度に基づく確率判定で投稿するか決定
4. 最近のチャンネル記憶 + 過去のつぶやきを取得（重複回避用）
5. LLM でつぶやき生成（時間帯ヒント + 退屈度付き、1回の LLM 呼び出し）
6. `cc.Notifier.Send` で送信
7. 長期記憶に保存

**設定例:**

```yaml
consolidator:
  scheduler:
    jobs:
      - name: "topics-hourly"
        task: "topics"
        cron: "0 * * * *"
        config:
          channel_id: "123456789"
```

### Explore / 自律探索 (`internal/explore/`)

定期的にネットを自律的に探索し、面白いものを記憶に保存するタスク。

**設計思想:** のの自身の好奇心でネットを駆け回る。Wikipedia ランダム記事を入り口に、気になったキーワードを SearXNG で検索し、WebFetch でページを読んで連想的に探索を続ける。面白かったものは長期記憶に保存され、独り言や会話に自然に反映される。

**処理フロー:**

1. 起点を決める（未探索の興味があればそれ、なければ Wikipedia ランダム記事）
2. LLM（のの視点）が記事を評価: 感想・覚えるか・次に調べたいキーワード
3. 覚える価値あり → `MemoryTypeWorld`（`source="explore"`）に保存
4. 次のキーワードあり → SearXNG で検索 → 結果から気になるものを選ぶ → 読む → 2 に戻る
5. 満足 or 深さ上限（デフォルト 4 ホップ）→ 探索まとめを記憶に保存
6. 辿れなかったキーワードは `unexplored_interests` として次回の起点候補に保存

**依存:** SearXNG（セルフホスト検索エンジン）が `searxng_url` で指定したアドレスで動作している必要がある。

**設定例:**

```yaml
consolidator:
  scheduler:
    jobs:
      - name: "explore"
        task: "explore"
        cron: "0 */3 * * *"
        config:
          searxng_url: "http://localhost:8888"
          max_depth: 4
```

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

  topics/            # Feature: 独り言（退屈システム）
    feature.go       # Feature 実装
    task.go          # TopicsTask (退屈度ベース独り言生成)

  explore/           # Feature: 自律探索
    feature.go       # Feature 実装
    task.go          # ExploreTask (Wikipedia + SearXNG 連想探索)
    searxng.go       # SearXNG クライアント
    wikipedia.go     # Wikipedia ランダム記事取得

  notification/
    notifier.go      # Notifier インターフェース, SendResult, NopNotifier
    grpc_notifier.go # GRPCNotifier
    middleware.go    # Middleware, Chain, WithQuietHours
    server.go        # gRPC NotificationServer (Agent 側)

proto/
  notification/v1/
    notification.proto
```
