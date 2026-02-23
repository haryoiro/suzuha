---
applyTo: "*"
paths: "*"
---

# suzuha 詳細設計・コードアーキテクチャ

## Context

draft-spec.md で仕様が固まった。次に Go のパッケージ構成と各モジュールの責務を設計し、実装に移る。
目標は「複雑にしすぎず、拡張性のあるモジュール分割」。

## プロセス構成

suzuha は **2つの独立したプロセス**で構成する。

```
┌────────────────────┐       gRPC        ┌─────────────────────┐
│   suzuha-agent      │◄────────────────►│   suzuha-consolidator │
│                    │                   │                      │
│  イベントループ     │  圧縮リクエスト    │  短期記憶の取捨選択   │
│  LLM 対話          │  ──────────►      │  重要情報の抽出       │
│  ツール実行         │                   │  長期記憶への書き込み  │
│  ルーティング       │  ◄────────────   │                      │
│                    │  保持すべき        │                      │
│                    │  メッセージ一覧    │                      │
└────────┬───────────┘                   └──────────┬───────────┘
         │                                          │
         │ 読み取り                                   │ 読み書き
         ▼                                          ▼
    ┌─────────┐                                ┌─────────┐
    │memory.db│◄───────────────────────────────│memory.db│
    └─────────┘        同一ファイル              └─────────┘
```

| プロセス                | 責務                                                           |
| ----------------------- | -------------------------------------------------------------- |
| **suzuha-agent**        | イベント受信、LLM 対話、ツール実行、ルーティング、短期記憶保持 |
| **suzuha-consolidator** | 短期記憶の圧縮判断、重要情報抽出、長期記憶書き込み             |

### なぜ分離するか

- 記憶の整理は重い LLM 処理（要約・重要度判定）を伴う。メインの対話ループをブロックしない
- 将来的に整理の頻度やモデルを独立してチューニングできる
- consolidator を止めても agent は動き続ける（記憶整理が止まるだけ）

### プロセス間通信: gRPC

```protobuf
service Consolidator {
  // Agent がコンテキスト圧縮を要求
  rpc Compact(CompactRequest) returns (CompactResponse);
}

message CompactRequest {
  repeated Message messages = 1;  // 現在の短期記憶全体
  int32 target_count = 2;         // 残すメッセージ数の目標
}

message CompactResponse {
  repeated int32 keep_indices = 1;  // 保持するメッセージのインデックス
  repeated Memory memories = 2;     // 長期記憶に保存する抽出情報
}
```

## パッケージ構成

```
suzuha/
├── cmd/
│   ├── suzuha-agent/
│   │   └── main.go              # Agent プロセス エントリポイント
│   └── suzuha-consolidator/
│       └── main.go              # Consolidator プロセス エントリポイント
│
├── internal/
│   ├── agent/                   # エージェントコアループ
│   │   ├── agent.go             # Agent struct, Run() ループ, イベント処理
│   │   ├── context.go           # 短期記憶 ([]Message), コンテキストウィンドウ管理
│   │   ├── router.go            # LLM判断による出力先ルーティング
│   │   └── interest.go          # チャット選択応答（ルールベース + バッファ判定）
│   │
│   ├── consolidator/            # 記憶整理プロセス
│   │   ├── server.go            # gRPC サーバー実装
│   │   └── compact.go           # 圧縮ロジック（LLM で取捨選択・要約）
│   │
│   ├── proto/                   # gRPC プロトコル定義（生成コード含む）
│   │   └── consolidator.proto
│   │
│   ├── config/
│   │   └── config.go            # Config struct, YAML ロード
│   │
│   ├── event/
│   │   └── event.go             # Event struct, EventBus (chan ベース)
│   │
│   ├── chat/                    # チャットインターフェイス抽象 + 実装
│   │   ├── chat.go              # chat.Interface 定義
│   │   ├── discord/
│   │   │   └── discord.go       # Discord 実装 (discordgo)
│   │   └── cli/
│   │       └── cli.go           # CLI 実装 (stdin/stdout)
│   │
│   ├── llm/                     # LLM クライアント（any-llm-go の薄いラッパー）
│   │   ├── llm.go               # Client struct, Complete(), Embed()
│   │   └── convert.go           # suzuha Message ↔ providers.Message 変換
│   │
│   ├── tool/                    # ツールシステム
│   │   ├── tool.go              # Tool interface, ToolResult 型
│   │   ├── registry.go          # ToolRegistry（全ツール統合管理）
│   │   ├── builtin/
│   │   │   ├── fetch.go         # Fetch ツール
│   │   │   └── websearch.go     # WebSearch ツール
│   │   └── remote/
│   │       ├── client.go        # RemoteToolClient（ツールサーバー接続管理）
│   │       └── proxy.go         # ProxyTool（リモートツール → Tool interface）
│   │
│   ├── transport/               # ツールサーバーとの通信抽象
│   │   ├── transport.go         # Transport interface, JsonRpcMessage 型
│   │   ├── websocket.go         # WebSocket トランスポート
│   │   └── mcp.go               # MCP ブリッジ（stdio/HTTP → Transport）
│   │
│   ├── memory/                  # 長期記憶（SQLite）
│   │   ├── store.go             # Store interface
│   │   ├── sqlite.go            # SQLite 実装
│   │   ├── search.go            # ハイブリッド検索（vec + FTS5 + RRF）
│   │   └── schema.go            # テーブル定義, マイグレーション
│   │
│   ├── trigger/                 # プロアクティブ・アクショントリガー
│   │   ├── trigger.go           # Trigger interface, Manager
│   │   ├── cron.go              # 定期実行トリガー
│   │   └── condition.go         # 条件トリガー
│   │
│   └── observe/                 # オブザーバビリティ
│       ├── log.go               # slog ロガー設定
│       └── metrics.go           # Prometheus メトリクス
│
├── docs/
│   └── architecture.md          # この文書
├── config.yaml
├── go.mod
└── go.sum
```

## 依存関係グラフ

```
cmd/suzuha-agent                     cmd/suzuha-consolidator
       │                                    │
       ▼                                    ▼
    agent ──────► proto/consolidator   consolidator
    │ │ │          (gRPC client)        │    │
    │ │ │                               │    │
    │ │ └──────┐                        ▼    ▼
    ▼ ▼        ▼                       llm  memory
   llm memory chat
    │           │
    ▼           ▼
   tool      chat/*
    ▲       (discord, cli)
    │
 tool/*        event ← リーフ（依存なし）
(builtin,      config ← リーフ
 remote)       observe
    │
    ▼
 transport
```

- `event`, `tool/tool.go`, `config` がリーフパッケージ
- `agent` と `consolidator` は独立したプロセスで、gRPC で通信
- `memory` パッケージは両プロセスから使用（同一 DB ファイル）
- 循環依存なし
- embedding 関数は各 `main.go` でクロージャとして注入

## 主要インターフェイス

### chat.Interface（agent が consume）

```go
type Interface interface {
    Run(ctx context.Context) error
    Send(ctx context.Context, channel string, text string) error
}
```

### tool.Tool（llm, agent が consume）

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (*ToolResult, error)
}
```

### transport.Transport（remote が consume）

```go
type Transport interface {
    Connect(ctx context.Context) error
    Send(ctx context.Context, msg *JsonRpcMessage) error
    Receive(ctx context.Context) (*JsonRpcMessage, error)
    Close() error
}
```

### memory.Store（agent, consolidator が consume）

```go
type Store interface {
    Save(ctx context.Context, mem *Memory) error
    Search(ctx context.Context, query string, limit int) ([]Memory, error)
    SearchByType(ctx context.Context, query string, memType MemoryType, limit int) ([]Memory, error)
    Close() error
}
```

### Consolidator gRPC（agent が consume）

```go
// agent 側が使うクライアント interface
type ConsolidatorClient interface {
    Compact(ctx context.Context, messages []Message, targetCount int) (*CompactResult, error)
}
```

## 設計判断

### 1. チャット選択応答: ルールベース優先

```
メッセージ受信
  ├─ メンション/リプライ → 即座に応答
  ├─ それ以外 → バッファに追加
  └─ 数秒後、バッファをまとめて LLM に渡し「どれに応答すべきか」を1回で判定
```

### 2. 短期記憶: Go []Message スライス + 外部圧縮

- `[]Message` で保持。インメモリ SQLite は使わない
- コンテキストウィンドウ 80% で圧縮発動
- agent は consolidator に gRPC で圧縮リクエストを送信
- consolidator が LLM で取捨選択し、重要情報を長期記憶に保存
- agent は返された keep_indices で短期記憶を圧縮
- **consolidator が落ちている場合**: agent 側でフォールバック（古いメッセージから単純に切り捨て）

### 3. チャンネル・ユーザー情報の LLM への伝達

```go
content = fmt.Sprintf("[%s in #%s]: %s", m.UserName, m.Channel, m.Content)
```

any-llm-go の Message 型を拡張せず、content 文字列に埋め込む。

### 4. MCP ブリッジは Transport レイヤーの問題

ツール側から見ると全て `tool.Tool` interface。MCP 対応は `transport/mcp.go` に閉じ込める。

### 5. EventBus は Go channel

単一コンシューマ（agent ループ）なので `chan Event` で十分。

## エージェントループのデータフロー

```
Discord/CLI/Timer
      │
      ▼ Event
  EventBus (chan)
      │
      ▼
  agent.Run() ループ
      │
      ├─ 1. Event → Message に変換、短期記憶に追加
      ├─ 2. コンテキスト圧縮チェック（80%超なら consolidator に非同期リクエスト）
      ├─ 3. 応答判定（ルールベース → バッチ LLM 判定）
      ├─ 4. 長期記憶から関連情報を検索・注入
      ├─ 5. LLM 呼び出し（コンテキスト + ツール定義）
      ├─ 6. ツール呼び出しループ（LLM がツールを返す限り繰り返し）
      ├─ 7. ルーティング（LLM が宛先を判断）
      └─ 8. chat.Interface.Send() で応答送信
```

## 実装順序

1. **リーフパッケージ**: `event/`, `tool/tool.go`, `config/`, `observe/`
2. **proto**: `proto/consolidator.proto` → コード生成
3. **インフラ**: `llm/`, `memory/`, `transport/`
4. **ツール**: `tool/registry.go`, `tool/builtin/`, `tool/remote/`
5. **チャット**: `chat/chat.go`, `chat/cli/`, `chat/discord/`
6. **トリガー**: `trigger/`
7. **Consolidator**: `consolidator/`, `cmd/suzuha-consolidator/`
8. **Agent コア**: `agent/`, `cmd/suzuha-agent/`

## テスト方針

| パッケージ   | テスト方式                     | モック対象                                  |
| ------------ | ------------------------------ | ------------------------------------------- |
| agent        | ユニットテスト                 | LLM, Memory, ConsolidatorClient, Tool, Chat |
| consolidator | ユニットテスト                 | LLM, Memory                                 |
| llm          | 統合テスト or モック HTTP      | providers.Provider                          |
| memory       | 統合テスト (`:memory:` SQLite) | embedding 関数                              |
| tool/builtin | ユニットテスト                 | HTTP クライアント                           |
| tool/remote  | ユニットテスト                 | Transport (モック)                          |
| transport    | 統合テスト                     | テスト用 WebSocket サーバー                 |
| chat/discord | 統合テスト                     | discordgo.Session                           |
| chat/cli     | ユニットテスト                 | io.Reader/Writer                            |
| trigger      | ユニットテスト                 | EventBus                                    |
