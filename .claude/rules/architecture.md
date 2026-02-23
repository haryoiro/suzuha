---
applyTo: "*"
paths: "*"
---

# suzuha アーキテクチャ概要

## プロセス構成

3つの独立したプロセスで構成。共有リソースは memory.db（同一ファイル）。

```mermaid
graph LR
  agent[suzuha-agent<br/>イベントループ・LLM対話・ツール実行]
  consol[suzuha-consolidator<br/>記憶の取捨選択・重要情報抽出]
  admin[suzuha-admin<br/>管理画面 React SPA]
  db[(memory.db)]

  agent -- gRPC 圧縮要求 --> consol
  consol -- 保持一覧 --> agent
  agent -- 読み取り --> db
  consol -- 読み書き --> db
  admin -- HTTP プロキシ --> agent
  admin -- HTTP プロキシ --> consol
```

## パッケージ構成

エントリポイント: `cmd/suzuha-agent`, `cmd/suzuha-consolidator`, `cmd/suzuha-admin`
プロトコル定義: `proto/` → 生成コード `gen/`

コアパッケージ (`internal/`):
- `agent` — エージェントコアループ、短期記憶管理、応答判定
- `consolidator` — 記憶圧縮サービス (gRPC サーバー/クライアント)
- `llm` — LLM クライアント (Complete, Embed)、Message 型
- `memory` — 長期記憶 (SQLite + FTS5 + sqlite-vec)、マイグレーション
- `user` — ユーザー管理、プラットフォームリンク、親和度
- `chat` — プラットフォーム抽象 + 実装 (discord, cli)
- `tool` — ツールインターフェース、Registry、builtin ツール、remote ツール
- `transport` — リモートツール通信 (WebSocket, MCP)
- `admin` — 管理画面バックエンド (REST API + SPA 配信)
- `event` — EventBus (chan ベース)
- `config` — YAML 設定ロード
- `observe` — Prometheus メトリクス、slog、ログストリーミング

## 依存関係

```mermaid
graph TD
  agent --> llm & memory & chat & tool & user & gen/consolidator
  consolidator --> llm & memory
  admin --> memory & user
  llm --> tool
  tool/builtin & tool/remote --> transport
  chat/discord & chat/cli -.-> chat.Interface
```

- リーフ: `event`, `config`, `observe`（外部依存なし）
- `memory` は agent と consolidator の両方から使用（同一 DB）
- embedding 関数は `main.go` でクロージャとして注入（循環依存回避）

## 主要インターフェース

- `chat.Interface` (`chat/chat.go`) — Run, Send。プラットフォーム抽象
- `tool.Tool` (`tool/tool.go`) — Name, Description, InputSchema, Execute。詳細は `tools.md`
- `memory.Store` (`memory/store.go`) — Save, Search, SearchByType, SearchRecent。詳細は `data.md`
- `memory.AdminStore` — Store + List, Get, Update, Delete（管理画面用）
- `consolidator.Client` (`consolidator/consolidator.go`) — Compact。詳細は `data.md`
- `user.Store` (`user/user.go`) — Resolve, Get, UpdateDisplayName, UpdateAffinity, GetAffinity。詳細は `data.md`
- `transport.Transport` (`transport/transport.go`) — Connect, Send, Receive, Close。詳細は `tools.md`
