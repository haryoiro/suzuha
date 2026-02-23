---
applyTo: "**"
paths: "internal/tool/**,internal/transport/**"
---

# ツールシステム

## Tool インターフェース

`internal/tool/tool.go`:
- `Name() string` — ツール名（LLM に渡す識別子）
- `Description() string` — ツールの説明（LLM が使用判断に利用）
- `InputSchema() json.RawMessage` — JSON Schema 形式の入力定義
- `Execute(ctx, input json.RawMessage) (*ToolResult, error)` — 実行

`ToolResult`: `Content []Content` + `IsError bool`
- ヘルパー: `TextResult(text)`, `ErrorResult(msg)` で生成

## Registry

`internal/tool/registry.go`:
- スレッドセーフ `map[string]Tool`
- `Register(tool)` — ツール登録
- `Unregister(name)` — 削除
- `Get(name) (Tool, bool)` — 名前で取得
- `All() []Tool` — 全ツール取得（LLM にツール定義を渡す際に使用）

## 組込ツール

`internal/tool/builtin/`:

- **`fetch`** (`fetch.go`) — URL 取得。HTML レスポンスは `htmlconv.go` で Markdown に変換し 4000 文字に切り捨て。読み取り上限 512KB
- **`web_search`** (`websearch.go`) — SearXNG または Brave API で検索
- **`discord_react`** (`discord.go`) — Discord メッセージにリアクション追加
- **`discord_reply`** (`discord.go`) — Discord メッセージに返信
- **`discord_get_history`** (`discord.go`) — チャンネルの直近メッセージ取得（時系列順、`author_id` 付き）
- **`update_user_profile`** (`user_profile.go`) — ユーザー表示名を更新

Discord ツールは `discordgo.Session` に依存。`OnReady` コールバック経由で Discord 接続後に登録される。

## リモートツール

`internal/tool/remote/`:
- `RemoteToolClient` (`client.go`) — WebSocket/MCP でツールサーバーに接続、利用可能ツールを取得
- `ProxyTool` (`proxy.go`) — リモートツールを `Tool` interface にアダプト。`Execute` は JSON-RPC で転送

## トランスポート

`internal/transport/`:
- `Transport` インターフェース: `Connect`, `Send(*JsonRpcMessage)`, `Receive`, `Close`
- `websocket.go` — WebSocket トランスポート実装
- `mcp.go` — MCP ブリッジ（stdio/HTTP → Transport）。ツール側は全て `tool.Tool` に見える
