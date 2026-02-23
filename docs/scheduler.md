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
                              ├── LLM Client     (LLM 呼び出し)
                              ├── Memory Store   (長期記憶の読み書き)
                              ├── NotifyFunc     (Discord 通知)
                              ├── *sql.DB        (タスク固有テーブル)
                              └── Logger
```

### 通知パイプライン

CronTask が Discord にメッセージを送りたい場合、`CronContext.Notifier` を呼ぶ。
内部的には gRPC で Agent の `NotificationService` を経由し、`chat.Interface.Send()` に委譲される。

```
CronTask → Notifier(channelID, text, source)
  → gRPC SendMessage → Agent NotificationServer
    → chat.Interface.Send() → Discord API
```

Agent が落ちている場合はエラーを返す。タスクはログを残して次の tick で再試行できる。

## 現状の機能 (Phase 1)

- スケジューラ基盤（ジョブ登録・cron 式スケジューリング・実行・停止）
- 通知パイプライン（Consolidator → Agent → Discord）
- CronTask プラグインインターフェース
- TaskRegistry（`tool.Registry` と同じ設計）

**組み込み CronTask はまだない。** Phase 2 以降で RSS 監視、TODO リマインダーなどを追加予定。

## 設定

`config.yaml` の `consolidator` セクションに追加する。

```yaml
consolidator:
  address: "localhost:50051"        # 既存: gRPC アドレス
  agent_notify: "localhost:50052"   # Agent の通知用 gRPC アドレス
  scheduler:
    enabled: true                   # false でスケジューラ無効
    jobs:
      - name: "my-job"             # ジョブの表示名（ログに出る）
        task: "task_name"          # CronTask.Name() と一致させる
        cron: "*/30 * * * *"       # cron 式
        config:                    # タスク固有設定（JSON として Execute に渡る）
          key: "value"
```

### cron 式の書式

標準の 5 フィールド cron 式に加え、`robfig/cron/v3` の拡張構文が使える。

| 書式 | 意味 |
|------|------|
| `* * * * *` | 毎分 |
| `*/30 * * * *` | 30 分ごと |
| `0 9 * * *` | 毎日 9:00 |
| `0 9 * * 1-5` | 平日の 9:00 |
| `@every 10s` | 10 秒ごと |
| `@every 1h` | 1 時間ごと |
| `@daily` | 毎日 0:00 |
| `@hourly` | 毎時 0 分 |

### デフォルト値

| キー | デフォルト | 説明 |
|------|-----------|------|
| `consolidator.agent_notify` | `localhost:50052` | Agent の通知 gRPC アドレス |
| `consolidator.scheduler.enabled` | `false` | スケジューラを有効にするか |

## CronTask の実装方法

新しい定期ジョブを追加するには 3 ステップ。

### 1. CronTask インターフェースを実装

```go
// internal/scheduler/tasks/example.go
package tasks

import (
    "context"
    "encoding/json"
    "github.com/haryoiro/suzuha/internal/scheduler"
)

type ExampleTask struct{}

func (t *ExampleTask) Name() string        { return "example" }
func (t *ExampleTask) Description() string { return "サンプルタスク" }

func (t *ExampleTask) Setup(ctx context.Context, cc *scheduler.CronContext) error {
    // テーブル作成やデータ初期化など（不要なら何もしない）
    return nil
}

func (t *ExampleTask) Execute(ctx context.Context, cc *scheduler.CronContext, cfg json.RawMessage) error {
    // cfg から設定を読む
    var conf struct {
        ChannelID string `json:"channel_id"`
        Message   string `json:"message"`
    }
    if err := json.Unmarshal(cfg, &conf); err != nil {
        return err
    }

    // 利用できるサービス:
    // cc.LLM      — LLM に問い合わせ
    // cc.Memory   — 長期記憶を検索・保存
    // cc.Notifier — Discord に通知送信
    // cc.DB       — SQL を直接実行
    // cc.Logger   — ログ出力

    return cc.Notifier(ctx, conf.ChannelID, conf.Message, "example")
}
```

### 2. レジストリに登録

`cmd/suzuha-consolidator/main.go` の `taskRegistry` に追加:

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
          message: "定期お知らせだよ"
```

## CronContext で使えるサービス

| フィールド | 型 | 用途 |
|-----------|-----|------|
| `LLM` | `*llm.Client` | LLM 呼び出し (Complete, Embed) |
| `Memory` | `memory.Store` | 長期記憶の検索・保存 |
| `Notifier` | `NotifyFunc` | Discord チャンネルへの通知送信 |
| `DB` | `*sql.DB` | SQLite への直接クエリ（タスク固有テーブル用） |
| `Logger` | `*slog.Logger` | 構造化ログ |

## ファイル構成

```
internal/
  scheduler/
    task.go          # CronTask interface, CronContext
    registry.go      # TaskRegistry
    scheduler.go     # Scheduler (robfig/cron/v3 ラッパー)

  notification/
    client.go        # NotifyFunc, NewGRPCNotifier (Consolidator 側)
    server.go        # gRPC NotificationServer (Agent 側)

proto/
  notification/v1/
    notification.proto  # NotificationService 定義
```

## 今後の計画

- **Phase 2: RSS 監視** — フィード登録、新着記事の取得、LLM による興味スコアリング、Discord 通知
- **Phase 3: TODO リマインダー** — ユーザー/チャンネル単位の TODO 管理、期日リマインド、チャットからの自動作成
- **Phase 4: 管理画面統合** — Admin UI でジョブ状態の確認、RSS フィード管理、TODO 一覧
