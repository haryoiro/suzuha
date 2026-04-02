# ツールシステム

エージェントは LLM が呼び出せるツールを通じて外部世界と対話する。ツールは複数のソースから登録され、ランタイムで有効/無効を切り替えられる。

## ツールインターフェース

```go
// internal/tool/tool.go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (*ToolResult, error)
}

type ToolResult struct {
    Content   []Content  // テキストコンテンツ
    IsError   bool       // エラーの場合 true
    StopAfter bool       // true ならツールループを停止（追加の LLM 呼び出しなし）
    ImageURLs []string   // 画像 data URI（tool result メッセージに付加）
}
```

### 特殊な ToolResult

- `TextResult(text)`: 通常のテキスト結果
- `StopResult(text)`: ツールループを即座に停止する結果（リアクション等の副作用のみのツール用）
- `ErrorResult(msg)`: エラー結果

## ツール一覧

### Builtin ツール（`internal/tool/builtin/`）

| ツール名 | 説明 | ファイル |
|---------|------|---------|
| `fetch` | URL からウェブページのテキストを取得 | `fetch.go` |
| `python_exec` | Python コードを実行 | `python.go` |
| `update_user_profile` | ユーザーの表示名を更新 | `user_profile.go` |

### Discord ツール（`internal/tool/builtin/discord.go`、OnReady 時に登録）

| ツール名 | 説明 |
|---------|------|
| `discord_react` | メッセージにリアクション（StopAfter） |
| `discord_reply` | メッセージにリプライ |
| `discord_get_history` | チャンネルの直近メッセージ取得 |
| `discord_send_dm` | ユーザーに DM 送信 |
| `discord_create_channel` | チャンネル作成 |
| `discord_edit_channel` | チャンネル編集 |
| `discord_delete_channel` | チャンネル削除 |
| `discord_list_channels` | チャンネル一覧 |
| `discord_kick_member` | メンバーキック |
| `discord_ban_member` | メンバー BAN |
| `discord_timeout_member` | メンバータイムアウト |
| `discord_list_members` | メンバー一覧 |
| `discord_delete_message` | メッセージ削除 |
| `discord_pin_message` | メッセージピン留め |
| `discord_add_role` | ロール付与 |
| `discord_remove_role` | ロール除去 |
| `discord_list_roles` | ロール一覧 |
| `discord_server_info` | サーバー情報取得 |
| `discord_create_thread` | スレッド作成 |
| `discord_rename_server` | サーバー名変更 |
| `discord_set_nickname` | ニックネーム設定 |
| `discord_update_status` | Bot ステータス更新 |

### Voice ツール（音声有効時に OnReady で登録）

| ツール名 | 説明 |
|---------|------|
| `voice_join` | VC に参加 |
| `voice_leave` | VC を退出 |

### Device ツール（物理デバイス、`internal/device/tools.go`）

| ツール名 | 説明 |
|---------|------|
| `body_turn_head` | サーボで首を動かす（pan/tilt） |
| `body_blink` | カメラでスナップショット撮影 |
| `body_expression` | 表情変更（0=通常〜7=喋り中） |
| `body_look` | 視界認識（最新フレームを VLM で記述） |

### Feature ツール

各 Feature が `Tools()` メソッドで返すツール:

| Feature | ツール | 説明 |
|---------|--------|------|
| Schedule (`internal/feature/action/`) | `schedule_create`, `schedule_list`, `schedule_cancel` | 予約アクション管理 |
| MCP Apps (`internal/mcp/`) | `mcp_search`, `mcp_install`, `mcp_uninstall`, `mcp_list_apps` | MCP ツールサーバー管理 |
| Research (`internal/feature/research/`) | `research` | ウェブ検索（SearXNG + ページ取得） |
| Wander (`internal/feature/wander/`) | `wander` | 好奇心探索（SearXNG + LLM 評価） |
| Location (`internal/location/`) | ロケーション関連ツール | GPS 位置情報 |

### MCP ツール（`internal/mcp/`）

MCP (Model Context Protocol) 対応のツールサーバーに接続し、サーバーが提供するツールを動的に登録。

**接続方式:**
- `stdio`: サブプロセスとして起動
- `http`: HTTP ベースの StreamableHTTP transport

config.yaml で静的に定義するか、`mcp_install` ツールで動的にインストール可能。インストールしたアプリは DB に永続化され、再起動時に自動再接続される。

### 仮想ツール: `skip_response`

`[RESPOND]` ディレクティブ以外の場合に自動注入される仮想ツール。LLM が「この会話には返答しない」と判断した場合に呼ぶ。`discord_react` と併用可能（リアクションだけして返答しない）。

```go
type skipResponseTool struct{}
func (skipResponseTool) Name() string        { return "skip_response" }
func (skipResponseTool) Description() string {
    return "この会話に返答しないときに呼ぶ。テキストを返す場合はこのツールを呼ばないこと。
             discord_react と一緒に呼んでもよい。"
}
func (skipResponseTool) Execute(ctx, input) (*tool.ToolResult, error) {
    return tool.StopResult("skipped"), nil
}
```

## ツール管理

### 有効/無効切り替え

- Admin API: `PUT /internal/tools/{name}/enabled`
- 無効化されたツールは `registry.AllEnabled()` から除外される
- 状態は `app_settings` テーブルに永続化

### Tool Registry（`internal/tool/registry.go`）

```go
type Registry struct {
    tools    map[string]Tool  // 全登録ツール
    disabled map[string]bool  // 無効化されたツール名
}

func (r *Registry) Register(t Tool)          // ツール登録
func (r *Registry) All() []Tool              // 全ツール
func (r *Registry) AllEnabled() []Tool       // 有効ツールのみ
func (r *Registry) IsDisabled(name) bool     // 無効か
func (r *Registry) SetDisabled(names []string) // 一括無効化
```
