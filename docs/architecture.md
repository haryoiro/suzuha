# suzuha アーキテクチャ全体図

## システム全体構成

Go 1.26 製の Discord ボット。LLM 対話・長期記憶・定期タスクを 3 プロセス構成で実現する。

```mermaid
graph TB
  subgraph External["外部サービス"]
    discord["Discord API"]
    llmapi["LLM API<br/>(GLM-4.7 / OpenAI)"]
    searxng["SearXNG<br/>(セルフホスト検索)"]
  end

  subgraph Processes["suzuha プロセス群"]
    agent["suzuha-agent<br/>:9090 HTTP / :50052 gRPC"]
    consol["suzuha-consolidator<br/>:50051 gRPC"]
    admin["suzuha-admin<br/>:8080 HTTP"]
  end

  db[(memory.db<br/>SQLite WAL)]
  frontend["React SPA<br/>(Ant Design)"]

  discord <--> agent
  agent -- "gRPC: 圧縮要求" --> consol
  consol -- "gRPC: 通知" --> agent
  agent <--> llmapi
  consol <--> llmapi
  consol --> searxng
  agent -- "読み書き" --> db
  consol -- "読み書き" --> db
  admin -- "読み取り" --> db
  admin -- "HTTP プロキシ<br/>(ログ/コンテキスト/圧縮)" --> agent
  frontend -- "REST API" --> admin
```

### プロセス一覧

| プロセス | 役割 | ポート |
|---------|------|--------|
| **suzuha-agent** | Discord イベント受信・LLM 対話・ツール実行・応答送信 | 9090 (HTTP), 50052 (gRPC) |
| **suzuha-consolidator** | 記憶圧縮 (gRPC) + Cron スケジューラ（RSS/独り言/探索） | 50051 (gRPC) |
| **suzuha-admin** | 管理画面 REST API + React SPA 配信 | 8080 (HTTP) |

3 プロセスは同一 SQLite DB (`memory.db`) を WAL モードで共有する。

---

## パッケージ構成

```
cmd/
  suzuha-agent/           # Agent エントリポイント
  suzuha-consolidator/    # Consolidator エントリポイント
  suzuha-admin/           # Admin エントリポイント

internal/
  ┌─ コア ──────────────────────────────────
  │  agent/               エージェントコアループ・短期記憶管理・応答判定
  │  consolidator/        記憶圧縮サービス (gRPC サーバー/クライアント)
  │  admin/               管理画面バックエンド
  │    handler/           REST API ハンドラ群
  │    middleware/         CORS, ログ
  │
  ├─ データ ─────────────────────────────────
  │  memory/              長期記憶 (SQLite + FTS5 + sqlite-vec)
  │    migrations/        goose SQL マイグレーション
  │  user/                ユーザー管理・プラットフォームリンク・親和度
  │
  ├─ プラットフォーム抽象 ──────────────────
  │  chat/                Interface, Replier, IDSender
  │    discord/           Discord 実装 (discordgo)
  │    cli/               CLI 実装 (stdin/stdout)
  │
  ├─ LLM・ツール ────────────────────────────
  │  llm/                 LLM クライアント (Complete, Embed)
  │  tool/                ツール Registry + Tool インターフェース
  │    builtin/           組み込みツール群
  │  transport/           リモートツール通信 (WebSocket, MCP)
  │
  ├─ Feature (プラグイン) ──────────────────
  │  rss/                 RSS フィード監視（ツール + タスク + DB）
  │  topics/              独り言（退屈度ベース、タスクのみ）
  │  explore/             自律探索（Wikipedia + SearXNG、タスクのみ）
  │  schedule/            スケジュール管理
  │
  ├─ インフラ ───────────────────────────────
  │  scheduler/           CronTask, CronContext, Feature, Registry
  │  notification/        統一 Notifier + Middleware + gRPC サーバー
  │  event/               EventBus (chan ベース)
  │  observe/             SQLite メトリクス + slog + ログストリーミング
  │  config/              YAML 設定ロード
  │
proto/                    Protobuf 定義
gen/                      Protobuf 生成コード
web/admin/                React SPA (Vite + Ant Design)
```

### パッケージ依存関係

```mermaid
graph TD
  agent --> llm & memory & chat & tool & user & event & observe
  agent --> gen_consol["gen/consolidator"]
  consolidator --> llm & memory & notification & scheduler
  admin --> memory & user

  scheduler --> notification
  rss --> scheduler & tool & memory
  topics --> scheduler & memory
  explore --> scheduler & memory

  notification -.-> |Optional Interface| chat
  tool_builtin["tool/builtin"] --> transport
  chat_discord["chat/discord"] -.-> chat
  chat_cli["chat/cli"] -.-> chat

  classDef leaf fill:#e8f5e9,stroke:#43a047
  class event,config leaf
```

---

## メッセージ処理フロー（Agent コアループ）

Discord メッセージ受信から応答送信までの全体フロー。

```mermaid
flowchart TD
  A["Discord MessageCreate"] --> B["chat/discord: messageToEvent()"]
  B --> C["EventBus.Publish()"]
  C --> D["agent.Run() イベントループ"]
  D --> E["handleEvent()"]

  E --> E1["1. Event → llm.Message 変換"]
  E1 --> E2["2. users.Resolve() ユーザー特定"]
  E2 --> E3["3. channel_activity 更新"]
  E3 --> E4["4. injectChannelHistory()<br/>未見チャンネルの履歴注入"]
  E4 --> E5["5. ctx.Add(msg) 短期記憶に追加"]
  E5 --> E6["6. injectUserProfile()<br/>初回のみユーザー情報注入"]

  E6 --> E7{"UsageRatio > 80%?"}
  E7 -- Yes --> compact["compact() → Consolidator gRPC"]
  E7 -- No --> E8
  compact --> E8["7. injectMemories()<br/>ハイブリッド検索で関連記憶注入"]

  E8 --> E9["8. responseDirective()<br/>[RESPOND] or [LISTEN]"]
  E9 --> E10["9. completeWithTools()<br/>LLM 呼び出し + ツールループ (最大10回)"]

  E10 --> E11{"[SKIP] or 空?"}
  E11 -- Yes --> E13["スキップ（無応答）"]
  E11 -- No --> E12["10. chat.Send() → Discord 送信"]
  E12 --> E14["11. persistContext() DB 永続化"]
  E13 --> E14
```

### 応答判定ロジック

```
メッセージ種別の判定
├── DM / @メンション / CLI / トリガー → [RESPOND] 「必ず返答」
└── 通常チャンネルメッセージ           → [LISTEN] 「混ざりたければ返答、不要なら [SKIP]」
```

---

## 記憶圧縮パイプライン

コンテキストウィンドウの 80% を超えると Consolidator に圧縮を依頼する。

```mermaid
sequenceDiagram
  participant A as Agent
  participant C as Consolidator
  participant DB as SQLite

  A->>C: gRPC Compact(messages, targetCount)
  C->>C: buildCompactPrompt()
  C->>C: LLM.CompleteRaw() — 取捨選択
  C->>C: parseCompactResponse()
  Note over C: KEEP: 保持するメッセージ index<br/>MEMORIES: 長期記憶に昇格<br/>AFFINITY: ユーザー親和度変化

  loop 抽出された各 Memory
    C->>DB: IsDuplicate() (コサイン距離 < 0.15)
    alt 新規
      C->>DB: Save(memory + embedding)
    end
  end

  C-->>A: CompactResult{KeepIndices, Memories, AffinityDeltas}
  A->>A: ctx.ReplaceAll(kept) — 短期記憶を圧縮
  A->>A: applyAffinityDeltas() — 親和度更新
```

### フォールバック

Consolidator 不通時 → Agent 側で `TruncateOldest()` により古いメッセージを切り捨て。

---

## 通知パイプライン（Consolidator → Discord）

Consolidator の定期タスクから Discord にメッセージを送信するフロー。

```mermaid
flowchart LR
  task["CronTask<br/>(RSS/Topics/Explore)"]
  notifier["Notifier<br/>Send() / Reply()"]
  quiet["QuietHours<br/>Middleware<br/>(23:00–08:00 抑制)"]
  grpc["gRPC<br/>NotificationService"]
  server["Agent 側<br/>NotificationServer"]
  chat["chat.Interface"]
  discord["Discord API"]

  task --> notifier --> quiet --> grpc --> server --> chat --> discord
```

### Agent 側のルーティング

```
reply_to_message_id あり？
├── Yes → chat が Replier？
│     ├── Yes → SendReply()
│     └── No  → Send() (フォールバック)
└── No  → chat が IDSender？
      ├── Yes → SendWithID()
      └── No  → Send()
```

---

## 長期記憶（ハイブリッド検索）

```mermaid
flowchart LR
  query["検索クエリ"]
  fts["FTS5<br/>キーワード検索"]
  vec["sqlite-vec<br/>ベクトル KNN"]
  rrf["RRF マージ<br/>(k=60)"]
  result["ランキング結果"]

  query --> fts --> rrf
  query --> vec --> rrf
  rrf --> result
```

| 記憶タイプ | 用途 |
|-----------|------|
| `user` | ユーザーに関する情報（metadata に `user_id`） |
| `world` | 一般的な知識・事実 |
| `tool` | ツール使用パターン・結果 |
| `rss` | RSS 記事 |

### テーブル構成

- `memories` — メインテーブル (id, type, content, metadata JSON, timestamps)
- `memories_fts` — FTS5 全文検索インデックス (trigram)
- `memories_vec` — sqlite-vec ベクトルインデックス (1024次元 float32)

重複判定: コサイン距離 < 0.15 で同一記憶と判定。

---

## Feature（プラグイン）パターン

各機能は `scheduler.Feature` インターフェースを実装し、ツール・タスク・DB セットアップを 1 パッケージにまとめる。

```mermaid
classDiagram
  class Feature {
    <<interface>>
    +Name() string
    +Setup(ctx, db) error
    +Tools() []Tool
    +Tasks() []CronTask
  }

  class CronTask {
    <<interface>>
    +Name() string
    +Description() string
    +Setup(ctx, CronContext) error
    +Execute(ctx, CronContext, config) error
  }

  class CronContext {
    LLM *llm.Client
    Memory memory.Store
    Notifier notification.Notifier
    DB *sql.DB
    Logger *slog.Logger
    Timezone *time.Location
    SystemPrompt string
  }

  Feature --> CronTask : Tasks()
  CronTask --> CronContext : Execute() で利用
```

### 組み込み Feature 一覧

| Feature | パッケージ | Agent ツール | Cron タスク | 概要 |
|---------|-----------|-------------|-------------|------|
| **RSS** | `internal/rss/` | subscribe, unsubscribe, list, preference | `*/30 * * * *` | フィード監視 → ベクトルフィルタ → LLM スコアリング → 通知 |
| **Topics** | `internal/topics/` | なし | `0 * * * *` | 退屈度ベースの独り言生成 |
| **Explore** | `internal/explore/` | なし | `0 */3 * * *` | Wikipedia → SearXNG 連想探索 → 記憶保存 |
| **Schedule** | `internal/schedule/` | あり | あり | スケジュール管理 |

---

## 管理画面

### バックエンド API

```
/api/
├── health                          ヘルスチェック
├── memories/                       長期記憶 CRUD + 検索
│   ├── {id}
│   ├── vec-stats                   ベクトル埋め込み統計
│   ├── with-vec                    埋め込み有無付きリスト
│   └── duplicates                  類似記憶検出
├── users/                          ユーザー管理
│   └── {id}/
│       ├── affinity                親和度履歴
│       ├── guilds                  ギルド/チャンネル
│       └── memories                ユーザー関連記憶
├── guilds/                         ギルド一覧
│   └── {id}/channels
├── channels/                       チャンネル一覧
├── metrics/json                    SQLite メトリクス
├── scheduled-actions/              スケジュールアクション CRUD
├── feeds/                          RSS フィード CRUD
│   ├── {id}/items
│   └── stats
├── prompts/                        IDENTITY.md / SOUL.md 編集
│   └── {name}
├── context                         Agent 短期記憶プロキシ
├── logs/stream                     SSE ログストリーム
└── agent/compact                   強制圧縮プロキシ
```

### フロントエンド (React SPA)

```
web/admin/src/
├── routes/
│   ├── index.tsx           Dashboard（概要）
│   ├── memories/           長期記憶管理
│   │   ├── index.tsx       一覧 + 検索
│   │   └── $id.tsx         詳細・編集
│   ├── feeds/              RSS フィード管理
│   ├── users/              ユーザー一覧・詳細・親和度
│   ├── actions.tsx         スケジュールアクション
│   ├── prompts.tsx         プロンプト編集
│   ├── metrics.tsx         メトリクス可視化
│   ├── context.tsx         短期記憶ビューア
│   └── logs.tsx            リアルタイムログ (SSE)
├── hooks/                  API 呼び出しフック
└── lib/api.ts              REST API クライアント
```

---

## 主要インターフェース一覧

| インターフェース | 定義場所 | 実装 | 役割 |
|----------------|---------|------|------|
| `chat.Interface` | `chat/chat.go` | discord, cli | プラットフォーム抽象 (Run, Send) |
| `chat.Replier` | `chat/chat.go` | discord | リプライ送信 (Optional) |
| `chat.IDSender` | `chat/chat.go` | discord | メッセージ ID 付き送信 (Optional) |
| `tool.Tool` | `tool/tool.go` | builtin/* | ツール実行 |
| `memory.Store` | `memory/store.go` | SQLiteStore | 長期記憶の検索・保存 |
| `memory.AdminStore` | `memory/store.go` | SQLiteStore | 管理画面用 CRUD |
| `consolidator.Client` | `consolidator/` | GRPCClient | 記憶圧縮要求 |
| `notification.Notifier` | `notification/` | GRPCNotifier, NopNotifier | 統一通知 |
| `scheduler.Feature` | `scheduler/` | rss, topics, explore, schedule | 機能プラグイン |
| `scheduler.CronTask` | `scheduler/` | 各 Feature 内 Task | 定期実行ジョブ |
| `user.Store` | `user/user.go` | SQLiteStore | ユーザー管理・親和度 |

---

## Docker 構成

```yaml
# docker-compose.yaml
services:
  agent:          # Go — ポート 9090
  consolidator:   # Go — gRPC 50051 (内部)
  admin:          # Go — ポート 8080
  admin-frontend: # Node 22 — Vite dev server 5173
  searxng:        # セルフホスト検索エンジン
```

DB (`memory.db`) は `./data:/data` ボリュームで agent, consolidator, admin が共有。

---

## 主要ライブラリ

| ライブラリ | 用途 |
|-----------|------|
| `bwmarrin/discordgo` | Discord API |
| `mozilla-ai/any-llm-go` | LLM プロバイダ抽象 (OpenAI 互換) |
| `mattn/go-sqlite3` | SQLite ドライバ (CGO) |
| `asg017/sqlite-vec-go-bindings` | sqlite-vec ベクトル検索 |
| `pressly/goose/v3` | DB マイグレーション |
| `robfig/cron/v3` | Cron スケジューラ |
| `google.golang.org/grpc` | gRPC (Agent ↔ Consolidator) |
