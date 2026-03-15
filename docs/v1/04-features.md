# Feature システム

Feature はスケジューラータスク、エージェントツール、DB セットアップをバンドルする自己完結的なモジュールである。各 Feature は `scheduler.Feature` インターフェースを実装する。

## Feature インターフェース

```go
// internal/scheduler/feature.go
type Feature interface {
    Name() string
    Setup(ctx context.Context, db *sql.DB) error  // DB テーブル作成等（冪等）
    Tools() []tool.Tool                           // エージェントツール
    Tasks() []CronTask                            // スケジューラータスク
}
```

Feature が `agent.PipelineHook` も実装していれば、パイプラインフックとして自動登録される。

## 登録フロー

`cmd/suzuha-agent/providers.go` で全 Feature をインスタンス化:

```go
features := []scheduler.Feature{
    action.New(store.DB()),
    mcp.NewFeature(mcpMgr, logger),
    topics.New(),
    explore.New(searxURL, llmClient, store, systemPrompt, maxDepth),
    forget.New(),
    location.NewFeature(locStore),  // 有効時のみ
}

for _, f := range features {
    f.Setup(ctx, db)             // DB セットアップ
    for _, t := range f.Tools() {
        registry.Register(t)     // ツール登録
    }
    if h, ok := f.(agent.PipelineHook); ok {
        ag.AddHook(h)            // パイプラインフック登録
    }
}
```

---

## 各 Feature の詳細

### 1. Topics（独り言）

**パッケージ:** `internal/topics/`

Cron で定期的に実行され、退屈度に基づいて自発的に発言する。

**退屈度計算:**
```
boredom = hours_since_last_interaction × 8.0  (上限 100)
```

| 退屈度 | 意味 |
|--------|------|
| 0-20 | まだ暇じゃない → 投稿しない |
| 20-50 | ちょっと暇 → 低確率で投稿 |
| 50-80 | そこそこ暇 → 中確率で投稿 |
| 80-100 | かなり暇 → 高確率で投稿（最大 85%） |

**処理フロー:**
1. 最後のユーザーインタラクション時刻を取得
2. 退屈度を計算、確率的に投稿するか判定
3. コンテキスト収集: 最近の記憶、過去の独り言
4. メンション対象をinterest加重で選択（退屈度が高い場合）
5. セルフプロンプトイベントをイベントバスに発行

**メンション選択:** 退屈度 ≥ 50（高 interest ユーザーがいる場合は ≥ 30）で確率的にメンション。interest をウェイトとした重み付きランダム。

### 2. Explore（ウェブ探索）

**パッケージ:** `internal/explore/`

SearXNG メタ検索エンジンを使ってウェブを探索し、LLM で内容を評価する。エージェントのツールとしても、cron タスクとしても使える。

**探索フロー:**
1. SearXNG で検索 or Wikipedia 記事取得
2. LLM に検索結果を見せ、どれが気になるか番号で選ばせる
3. 選んだ記事の内容を LLM に評価させる（感想、記憶すべきか、次の検索クエリ）
4. `remember: true` なら記憶に保存
5. `next_query` があれば次の検索に進む（max_depth まで）
6. 最後に探索全体を LLM に要約させる

### 3. Forget（記憶重複削除）

**パッケージ:** `internal/forget/`

類似度の高い記憶を検出し、マージまたは削除する。Union-Find アルゴリズムでグルーピング。

**Admin API 経由の操作:**
- `GET /api/forget/groups`: 重複グループ一覧
- `POST /api/forget/delete`: 指定 ID の記憶を削除
- `POST /api/forget/merge`: 複数の記憶をマージ
- `POST /api/forget/run`: 自動重複削除実行

### 4. Schedule（予約アクション）

**パッケージ:** `internal/action/`

特定の日時やcron式で発言を予約する。

**ツール:** `schedule_create`, `schedule_list`, `schedule_cancel`

### 5. MCP Apps

**パッケージ:** `internal/mcp/`

MCP (Model Context Protocol) ツールサーバーの管理。

**ツール:**
- `mcp_search`: MCPHub レジストリからサーバーを検索
- `mcp_install`: サーバーをインストール・接続
- `mcp_uninstall`: サーバーをアンインストール
- `mcp_list_apps`: インストール済みアプリ一覧

**永続化:** インストールしたアプリは DB に保存され、再起動時に自動再接続。

### 6. Location

**パッケージ:** `internal/location/`

Overland アプリからの GPS データ受信。デバイスと場所の管理。

**コンテキスト注入:** Think ステージで `locationStore.BuildContextSnippet()` を呼んで位置情報をエフェメラルメッセージに追加。

---

## CronTask インターフェース

```go
type CronTask interface {
    Name() string
    Description() string
    Setup(ctx context.Context, cc *CronContext) error
    Execute(ctx context.Context, cc *CronContext, cfg json.RawMessage) error
}
```

### CronContext

タスクが利用する共有リソース:

```go
type CronContext struct {
    LLM             *llm.Client
    Memory          memory.Store
    Notifier        notification.Notifier
    DB              *sql.DB
    Logger          *slog.Logger
    Users           user.Store
    ChannelActivity channel.ActivityStore
    MemoryAdmin     memory.AdminStore
    MediaStore      memory.MediaStore
    Bus             *event.Bus
    Timezone        *time.Location
    SystemPrompt    string
}
```

### スケジューラー設定

config.yaml の `consolidator.scheduler` で定義:

```yaml
consolidator:
  scheduler:
    enabled: true
    timezone: "Asia/Tokyo"
    quiet_hours:
      enabled: true
      start: "23:00"
      end: "08:00"
    jobs:
      - name: "独り言"
        task: topics
        cron: "@every 10m"
        config:
          channel_id: "123456"
      - name: "ウェブ探索"
        task: explore
        cron: "@every 2h"
        config:
          searxng_url: "http://searxng:8080"
          max_depth: 3
```

### 通知ミドルウェア

タスクの出力はミドルウェアチェーンを通過:
1. `ChatNotifier`: チャットインターフェースに送信
2. `WithQuietHours`: 静寂時間中は抑制
3. `WithChannelSettings`: チャンネル設定に基づいてフィルタリング
