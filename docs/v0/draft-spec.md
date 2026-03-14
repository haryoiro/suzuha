# suzuha - AI Agent 仕様書（ドラフト）

## 1. ビジョン

neuro-sama のようなヒューマノイド AI Agent を目指す。
コーディング支援ではなく、**人と接することを目的**としたエージェント。

## 2. 設計方針

| 方針         | 説明                                                             |
| ------------ | ---------------------------------------------------------------- |
| 拡張性       | Discord・CLI など、あらゆるインターフェイスと接続可能にする       |
| テスト容易性 | 各レイヤーをインターフェイスで分離し、モック差し替えを容易にする |
| モジュール性 | コア機能とオプション機能を明確に分離する                         |

## 3. 技術スタック

- **言語**: Go
- **LLM 接続**: [any-llm-go](https://github.com/mozilla-ai/any-llm-go)
- **オブザーバビリティ**: ログ・メトリクスを出力し Grafana で管理

## 4. アーキテクチャ概要

```
┌─────────────────────────────────────────────────┐
│                 Interface Layer                  │
│                 Discord / CLI                    │
├─────────────────────────────────────────────────┤
│                   Agent Core                     │
│       対話ループ / コンテキスト管理 / 判断        │
├──────────────┬──────────────┬───────────────────┤
│  Tool System │   Memory     │  Proactive Action │
│  (§5)        │   (§7)       │  (§8)             │
├──────────────┴──────────────┴───────────────────┤
│               LLM Provider (any-llm-go)         │
├─────────────────────────────────────────────────┤
│             Observability (§11)                  │
│         Logs / Metrics / Traces → Grafana       │
└─────────────────────────────────────────────────┘
```

## 5. ツールシステム

### 5.1 ビルトインツール

| ツール    | 説明                                                                   |
| --------- | ---------------------------------------------------------------------- |
| Fetch     | URL の内容を取得する。`format` パラメータで raw / readable を切り替え  |
| WebSearch | Web 検索を実行する（バックエンド: DuckDuckGo）                         |

参考: https://qiita.com/gomi1994/items/2370f16708fe4182ec1e

### 5.2 サードパーティツール

WebSocket + JSON-RPC 2.0 を一次プロトコルとし、MCP 互換はブリッジで対応する。
技術者がツールサーバーを実装し、suzuha に WebSocket で接続することでツールを追加できる。

#### 3層ツールアーキテクチャ

```
┌──────────────────────────────────────────────────────┐
│                     Agent Core                        │
│                                                       │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────┐ │
│  │ ビルトイン     │  │ suzuha ツール  │  │ MCP ブリッジ│ │
│  │ (Go interface)│  │ サーバー      │  │            │ │
│  │               │  │ (WebSocket    │  │ MCP Server │ │
│  │ Fetch         │  │  + JSON-RPC)  │  │ ↕ 変換     │ │
│  │ WebSearch     │  │               │  │ suzuha 形式 │ │
│  │               │  │ Tool A        │  │            │ │
│  │               │  │ Tool B        │  │            │ │
│  └──────────────┘  └──────────────┘  └────────────┘ │
│                                                       │
│  共通: JSON Schema による入出力定義                     │
│  共通: Transport interface で抽象化                     │
└──────────────────────────────────────────────────────┘
```

| 層 | 方式 | 用途 |
|----|------|------|
| ビルトイン | Go interface を直接実装 | コア機能（Fetch, WebSearch 等） |
| サードパーティ | WebSocket + JSON-RPC 2.0 | 外部拡張ツール（一次プロトコル） |
| MCP ブリッジ | MCP ↔ suzuha プロトコル変換 | 既存 MCP サーバーとの互換 |

#### Transport interface による抽象化

WebSocket を一次トランスポートとしつつ、将来 MCP が WebSocket トランスポートを追加した際にもそのまま差し替え可能にする。

```go
// Transport はツールサーバーとの通信を抽象化する
type Transport interface {
    Connect(ctx context.Context) error
    Send(ctx context.Context, msg *JsonRpcMessage) error
    Receive(ctx context.Context) (*JsonRpcMessage, error)
    Close() error
}

// 実装例:
// - WebSocketTransport  ... suzuha ネイティブ（一次）
// - MCPStdioTransport   ... MCP stdio ブリッジ
// - MCPHttpTransport    ... MCP Streamable HTTP ブリッジ
// - (将来) MCPWebSocketTransport ... MCP が WS 対応した場合
```

#### suzuha ツールプロトコル

WebSocket + JSON-RPC 2.0。低レイテンシの双方向通信でリアルタイムなツール呼び出し・通知を実現する。

**接続フロー**:

```
suzuha (Host)                    Tool Server
     │                               │
     │◄──── WebSocket 接続 ──────────│
     │                               │
     │──── initialize ──────────────►│
     │◄─── capabilities 応答 ────────│
     │                               │
     │──── tools/list ──────────────►│
     │◄─── ツール一覧 ──────────────│
     │                               │
     │──── tools/call ──────────────►│
     │◄─── 実行結果 ────────────────│
     │                               │
     │◄─── notifications ───────────│  (ツール変更通知等)
     │                               │
```

**ツール登録メッセージ（例）**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "translate",
        "description": "テキストを翻訳する",
        "inputSchema": {
          "type": "object",
          "properties": {
            "text": { "type": "string", "description": "翻訳するテキスト" },
            "target_lang": { "type": "string", "description": "翻訳先言語コード" }
          },
          "required": ["text", "target_lang"]
        }
      }
    ]
  }
}
```

**ツール呼び出しメッセージ（例）**:

```json
// リクエスト
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "translate",
    "arguments": { "text": "Hello", "target_lang": "ja" }
  }
}

// レスポンス
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "content": [{ "type": "text", "text": "こんにちは" }],
    "isError": false
  }
}
```

#### ビルトインツールの Go interface（例）

```go
// ビルトイン・サードパーティ共通の抽象
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (*ToolResult, error)
}

type ToolResult struct {
    Content []Content `json:"content"`
    IsError bool      `json:"isError"`
}
```

#### MCP ブリッジ

既存の MCP サーバーを suzuha から利用するための変換レイヤー。

- MCP の `tools/list` / `tools/call` を suzuha の内部 Tool interface に変換
- MCP のトランスポート（stdio / Streamable HTTP）を Transport interface でラップ
- Go SDK: `github.com/modelcontextprotocol/go-sdk/mcp` を使用

```yaml
# config.yaml
tool_servers:
  # suzuha ネイティブ（WebSocket）
  custom-tools:
    type: websocket
    url: "ws://localhost:9001"

  # MCP ブリッジ経由
  mcp-server-a:
    type: mcp
    transport: stdio
    command: "/path/to/mcp-server"
    args: ["--mode", "production"]
    env:
      API_KEY: "xxx"

  mcp-server-b:
    type: mcp
    transport: http
    url: "https://example.com/mcp"
```

#### ツール管理フロー

1. 起動時に `config.yaml` からツールサーバー一覧を読み込み
2. 各サーバーの `type` に応じた Transport を生成して接続
3. 各サーバーに `tools/list` を送信し、利用可能なツールを取得
4. ビルトインツール + 全サードパーティツールを統合し、LLM のツール定義として送信
5. LLM がツール呼び出しを返したら、適切なサーバーに振り分けて実行
6. 結果を LLM に返却
7. サーバーからの `notifications/tools/list_changed` でツール一覧を動的更新

## 6. 対話インターフェイス

各インターフェイスは共通のインターフェイス（interface）を実装する。

| プラットフォーム | 優先度 |
| ---------------- | ------ |
| Discord          | 高     |
| CLI              | 中     |

## 7. 記憶・ストレージ

### 7.1 記憶の分類

| 種類         | 説明                     | 例                                |
| ------------ | ------------------------ | --------------------------------- |
| ユーザー記憶 | 人間に関する情報         | 名前、好み、過去の会話の要約      |
| 世界知識     | 物事に対する学習済み知識 | 調べた事柄、教わったこと          |
| ツール記憶   | ツールの使用履歴・結果   | 過去の検索結果、成功/失敗パターン |

### 7.2 ストレージ方式

**決定: SQLite + sqlite-vec + FTS5**

エージェントの記憶ストレージとして、SQLite にベクトル検索拡張（sqlite-vec）と全文検索（FTS5）を組み合わせるハイブリッド方式を採用する。

#### 選定理由

| 観点             | 詳細                                                                                   |
| ---------------- | -------------------------------------------------------------------------------------- |
| 単一ファイル     | `.db` ファイル1つで全記憶を管理。バックアップ・移行が容易                              |
| ハイブリッド検索 | FTS5（キーワード検索）+ sqlite-vec（ベクトル類似検索）を Reciprocal Rank Fusion で統合 |
| 構造化データ     | SQL テーブルで3種の記憶を自然にモデリング可能                                          |
| Go 連携          | `ncruces/go-sqlite3`（WASM / CGO 不要）+ `asg017/sqlite-vec-go-bindings/ncruces`       |
| パフォーマンス   | 768次元ベクトル 100K 件で 75ms 未満。エージェント規模では十分                          |

### 7.3 短期記憶と長期記憶

```
┌───────────────────────┐        定期的に取捨選択        ┌────────────────────────┐
│       短期記憶         │  ─────────────────────────►   │       長期記憶          │
│   (Go in-memory)      │     重要な情報を長期化         │  (persistent SQLite)   │
│                       │                                │                        │
│  []Message スライス    │                                │  user_memories テーブル  │
│  - 全チャンネル統合    │  ◄─────────────────────────   │  world_knowledge テーブル│
│  - チャンネル情報付き  │     関連記憶を検索・復元       │  tool_memories テーブル  │
│  - ユーザー情報付き    │                                │                        │
│                       │                                │  + FTS5 インデックス     │
│                       │                                │  + vec0 仮想テーブル     │
└───────────────────────┘                                └────────────────────────┘
```

#### 短期記憶（Go in-memory）

- Go の `[]Message` スライスで会話コンテキストを保持
- 全 Discord チャンネルを**統一した1つのコンテキスト**として管理
- 各メッセージにチャンネル名・ユーザー情報を付与し、エージェントがチャンネルを認識できるようにする

```go
type Message struct {
    Role      string    // "user" | "assistant" | "system"
    Content   string
    UserID    string    // Discord ユーザーID
    UserName  string    // 表示名
    Channel   string    // チャンネル名
    Timestamp time.Time
}
```

#### コンテキストウィンドウ管理

- コンテキストウィンドウの上限に近づいたら（例: 80%）、**半分を取捨選択**する
- 重要と判断されたメッセージは長期記憶（SQLite）に書き込む
- 不要と判断されたメッセージは破棄
- 取捨選択の判断は LLM に委ねる（要約 + 重要度評価）

#### 長期記憶（persistent SQLite）

- 永続化 SQLite ファイルにユーザー・世界知識・ツール記憶を格納
- **検索フロー**: セマンティック検索（sqlite-vec） + キーワード検索（FTS5） → Reciprocal Rank Fusion で統合
- LLM が応答生成時に関連する長期記憶を検索し、コンテキストに注入

### 7.4 Discord コンテキスト情報

エージェントが Discord の構造を認識するために、以下の情報を保持する。

| 情報 | 説明 |
|------|------|
| チャンネル一覧 | 参加しているチャンネルの名前・ID・用途 |
| ユーザー一覧 | 各ユーザーの ID・表示名・参加チャンネル |
| チャンネル × ユーザー | どのユーザーがどのチャンネルにいるか |

これにより「#general で話していた田中さんに #random で返事する」のような横断的な対応が可能になる。

## 8. エージェントの振る舞い

### 8.1 パーソナリティ

- 初期段階では**システムプロンプト**で性格・口調を制御する（一般的なモデルを使用）
- 将来的にファインチューニング（Qwen3 等）への移行を検討

### 8.2 プロアクティブ・アクション

Agent 側から能動的にアクションを起こす仕組み。イベント駆動 + 定期プロンプトのハイブリッド方式。

#### アーキテクチャ

```
┌───────────────────────────────────────────┐
│            データソース（共通スキーマ）      │
│                                           │
│  Discord   CLI   Timer   Webhook   ...    │
│     │       │      │        │             │
│     └───────┴──────┴────────┘             │
│                  │                         │
│            共通イベント形式                 │
└──────────────────┬────────────────────────┘
                   ▼
┌──────────────────────────────────────────┐
│           イベントルーター                 │
│                                           │
│  1. イベントを受信                         │
│  2. LLM にイベント内容 + コンテキストを送信 │
│  3. LLM が判断・応答を生成                 │
│  4. 判断結果に基づき宛先に振り分け          │
│                                           │
│     ┌──────┬──────┬──────┬──────┐        │
│     ▼      ▼      ▼      ▼      ▼        │
│  Discord  CLI   ツール  Memory  ...       │
└──────────────────────────────────────────┘
```

#### 共通イベントスキーマ（例）

```go
type Event struct {
    ID        string            // イベント一意ID
    Source    string            // "discord" | "cli" | "timer" | "webhook"
    Type      string            // "message" | "heartbeat" | "trigger"
    Payload   map[string]any    // ソース固有のデータ
    Context   []Message         // 直近の会話コンテキスト
    Timestamp time.Time
}
```

#### トリガーの種類

| トリガー | 説明 | 例 |
|----------|------|----|
| **定期実行** | 設定ファイルで定義した間隔で定期的にプロンプトを送信 | 5分ごとに未読チェック、朝の挨拶 |
| **イベント条件** | 特定の条件が満たされたときに発火 | 未読メッセージ蓄積、特定キーワード検知 |
| **外部イベント** | 外部データソースからのリアルタイム通知 | Discord メンション、Webhook 受信 |

#### ルーティング

LLM がイベント内容を判断し、応答の宛先を決定する:

- イベントのソースに関わらず、LLM が最適な出力先を判断
- 例: Timer イベントで「ユーザーにリマインダー」→ Discord に送信
- 例: Discord メッセージで「調べて」→ WebSearch ツール実行 → 結果を Discord に返却

### 8.3 チャット選択応答

全メッセージに返信せず、関心度に基づいて取捨選択する。

- LLM がメッセージの関心度（返信すべきか）をスコアリング
- 閾値を超えたメッセージにのみ応答する
- メンション・リプライなど明示的な呼びかけは常に応答
- 無視したメッセージもコンテキストには含める（話の流れは追っている状態）

## 9. オプション機能

以下はコア機能には組み込まず、プラグイン/拡張として実装する。
コア機能から先に固めて、拡張性を確保した上で追加していく。

- **タスク管理**: ユーザーの TODO やリマインダーを管理
- **スキルシステム**: 特定のタスクに特化した振る舞いの定義（必要に応じて）
- **感情・気分システム**: 内部状態として「気分」を持ち、応答トーンに影響させる
- **人間関係トラッキング**: ユーザーごとの好感度・関係性を記憶に蓄積
- **話題の自発的展開**: 受け身でなく自分から話題を振る・脱線する
- **マルチエージェント**: 複数の人格（Evil Neuro 的な）を持てる設計
- **コンテンツフィルタリング**: 多層フィルタで安全性を確保

## 10. 設定ファイル一覧

| ファイル       | 役割                                                         |
| -------------- | ------------------------------------------------------------ |
| `config.yaml`  | アプリケーション設定（LLM プロバイダ、ツールサーバー、トリガー定義等） |
| `memory.db`    | 長期記憶（SQLite + sqlite-vec + FTS5）                       |

`HEARTBEAT.md`, `TOOLS.md`, `USER.md` は廃止。すべて `config.yaml` と `memory.db` に統合する。

- トリガー定義 → `config.yaml` の `triggers:` セクション
- ツール設定 → `config.yaml` の `tool_servers:` セクション
- ユーザー情報 → `memory.db` の `user_memories` テーブル

## 11. オブザーバビリティ

ログ・メトリクスを出力し、Grafana で監視する。

### 11.1 メトリクス

| メトリクス | 説明 |
|------------|------|
| LLM トークン使用量 | リクエストごとの入力/出力トークン数 |
| LLM レイテンシ | リクエストごとの応答時間 |
| コンテキストウィンドウ使用率 | 現在のコンテキスト占有率 |
| ツール呼び出し回数・成功率 | ツールごとの実行統計 |
| イベント処理数 | ソース別のイベント受信・処理数 |
| 記憶書き込み数 | 短期→長期の転送回数・件数 |

### 11.2 ログ

| レベル | 対象 |
|--------|------|
| ERROR | LLM API エラー、ツールサーバー接続失敗、SQLite エラー |
| WARN | コンテキストウィンドウ圧迫、ツールタイムアウト |
| INFO | イベント受信、LLM 応答、ツール実行結果 |
| DEBUG | プロンプト全文、メモリ検索結果、ルーティング判断 |

## 12. TODO / 未決事項

- [x] ストレージ方式の選定 → SQLite + sqlite-vec + FTS5
- [x] ファインチューニング → 初期はシステムプロンプトで対応。将来的に Qwen3 等を検討
- [x] プロアクティブ・アクションのトリガー設計 → イベント駆動 + 定期プロンプト、LLM判断によるルーティング
- [~] 音声インターフェイスの技術選定 → スコープ外（将来検討）
- [x] サードパーティツールのプラグイン仕様 → WebSocket + JSON-RPC 2.0、Transport interface で抽象化、MCP はブリッジで互換対応
