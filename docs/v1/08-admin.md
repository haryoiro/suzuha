# 管理ダッシュボード

React SPA + Go バックエンドによる管理インターフェース。エージェントの状態監視、記憶管理、設定変更を Web UI から行える。

## アーキテクチャ

```
┌──────────────────┐       ┌──────────────────┐       ┌──────────────────┐
│  Admin Frontend  │ ────→ │  Admin Server    │ ────→ │  Agent Internal  │
│  (React SPA)     │       │  :8080           │       │  Server :9090    │
│  Vite + Ant      │       │  /api/*          │       │  /internal/*     │
│  Design          │       │  ogen generated  │       │                  │
└──────────────────┘       └──────────────────┘       └──────────────────┘
                                    │
                                    ▼
                            ┌──────────────┐
                            │   SQLite DB  │
                            └──────────────┘
```

### 2 つの HTTP サーバー

1. **Internal Server（:9090）**: Agent プロセス内で起動。LLM 切り替え、コンテキスト操作、ツール管理、デバイス WebSocket など低レベルの操作を提供
2. **Admin Server（:8080）**: Admin パッケージで起動。ogen（OpenAPI コード生成）ベースの型付き API + プロキシで Internal Server に転送

## Admin Server（`internal/admin/`）

### ミドルウェア

```
リクエスト → BasicAuth → CORS → Logging → Handler
```

- `BasicAuth`: config.yaml の `admin.auth` で設定（オプション）
- `CORS`: 全オリジン許可
- `Logging`: アクセスログ

### API エンドポイント一覧

#### ogen 生成（TypeSpec 定義: `api/main.tsp`）

| メソッド | パス | 説明 |
|---------|------|------|
| GET | `/api/health` | ヘルスチェック |
| GET | `/api/memories` | 記憶一覧（ページネーション、フィルタ、検索） |
| GET | `/api/memories/:id` | 記憶詳細 |
| POST | `/api/memories` | 記憶作成 |
| PUT | `/api/memories/:id` | 記憶更新 |
| DELETE | `/api/memories/:id` | 記憶削除 |
| GET | `/api/memories/vec-stats` | ベクトル埋め込み統計 |
| GET | `/api/memories/with-vec` | 埋め込み有無付き記憶一覧 |
| GET | `/api/memories/duplicates` | 重複検出 |
| GET | `/api/users` | ユーザー一覧 |
| GET | `/api/users/:id` | ユーザー詳細 |
| PUT | `/api/users/:id` | ユーザー更新 |
| GET | `/api/users/:id/affinity` | 好感度イベント |
| GET | `/api/users/:id/guilds` | ユーザーのギルド一覧 |
| GET | `/api/users/:id/memories` | ユーザーの記憶 |
| GET | `/api/guilds` | ギルド一覧 |
| GET | `/api/guilds/:id/channels` | ギルドのチャンネル一覧 |
| GET | `/api/channels` | 全チャンネル一覧 |
| GET | `/api/channel-settings` | チャンネル設定一覧 |
| PUT | `/api/channel-settings/:id` | チャンネル設定更新 |
| DELETE | `/api/channel-settings/:id` | チャンネル設定削除 |
| GET | `/api/metrics/json` | メトリクス（JSON） |
| GET | `/api/prompts` | プロンプトファイル一覧 |
| GET | `/api/prompts/:name` | プロンプトファイル取得 |
| PUT | `/api/prompts/:name` | プロンプトファイル更新 |
| GET | `/api/scheduled-actions` | 予約アクション一覧 |
| POST | `/api/scheduled-actions` | 予約アクション作成 |
| PUT | `/api/scheduled-actions/:id` | 予約アクション更新 |
| DELETE | `/api/scheduled-actions/:id` | 予約アクション削除 |
| GET | `/api/boredom` | 退屈度ステータス |
| GET | `/api/identity` | Bot のアイデンティティ情報 |
| GET | `/api/forget/groups` | 記憶重複グループ |
| GET | `/api/forget/status` | 重複削除ステータス |
| POST | `/api/forget/delete` | 記憶削除 |
| POST | `/api/forget/merge` | 記憶マージ |
| POST | `/api/forget/run` | 自動重複削除実行 |
| GET | `/api/location/devices` | ロケーションデバイス一覧 |
| PUT | `/api/location/devices/:id` | ロケーションデバイス更新 |
| DELETE | `/api/location/devices/:id` | ロケーションデバイス削除 |
| GET | `/api/location/places` | 場所一覧 |
| POST | `/api/location/places` | 場所作成 |
| PUT | `/api/location/places/:id` | 場所更新 |
| DELETE | `/api/location/places/:id` | 場所削除 |
| GET | `/api/conversation-logs` | 会話ログ |

#### プロキシ（Internal Server 転送）

| メソッド | パス | 転送先 | 説明 |
|---------|------|--------|------|
| POST | `/api/agent/compact` | `/internal/compact` | コンテキスト圧縮 |
| GET | `/api/context` | `/internal/context` | コンテキスト取得 |
| GET | `/api/tools` | `/internal/tools` | ツール一覧 |
| PUT | `/api/tools/:name/enabled` | `/internal/tools/:name/enabled` | ツール有効/無効 |
| GET | `/api/llm` | `/internal/llm` | LLM プロバイダー情報 |
| PUT | `/api/llm` | `/internal/llm` | LLM プロバイダー切り替え |
| GET | `/api/logs/stream` | `/internal/logs` (SSE) | ログストリーム |
| GET | `/api/scheduler/jobs` | `/internal/scheduler/jobs` | スケジューラージョブ |
| POST | `/api/scheduler/trigger/:task` | `/internal/trigger/:task` | タスク手動実行 |
| GET | `/api/device/frame` | `/internal/device/frame` | カメラフレーム |
| GET | `/api/device/detections` | `/internal/device/detections` | 物体検出 SSE |
| GET/PUT | `/api/device/vision` | `/internal/device/vision` | 視界変化検出 |
| GET | `/api/voicevox/speakers` | `/internal/voicevox/speakers` | VOICEVOX 話者 |
| GET/PUT | `/api/voicevox/speaker` | `/internal/voicevox/speaker` | 現在の話者 |

## フロントエンド（`admin/`）

### 技術スタック

- **React** + **TypeScript**
- **Ant Design** (UI コンポーネント)
- **Vite** (ビルドツール)
- ハッシュベースルーティング（React Router 不使用）

### ページ一覧

| ページ | パス | 機能 |
|--------|------|------|
| Dashboard | `#` | 概要・統計 |
| Memories | `#memories` | 記憶一覧・CRUD・検索 |
| Memory Detail | `#memory/:id` | 記憶詳細・編集 |
| Discord | `#discord` | チャンネル設定・ギルド管理 |
| Users | `#users` | ユーザー管理・好感度 |
| Actions | `#actions` | 予約アクション管理 |
| Location | `#location` | GPS デバイス・場所管理 |
| Tools | `#tools` | ツール一覧・有効/無効切り替え |
| Scheduler | `#scheduler` | cron ジョブ一覧・手動実行 |
| Prompts | `#prompts` | プロンプトファイル編集 |
| Metrics | `#metrics` | メトリクスグラフ |
| Device | `#device` | カメラフレーム表示・YOLO 検出・視界変化検出 |
| Voice | `#voice` | VOICEVOX 話者選択 |
| Context | `#context` | エージェントコンテキスト（メッセージ履歴 + エフェメラル）表示 |
| Logs | `#logs` | リアルタイムログ（SSE） |

### API クライアント（`admin/src/lib/api.ts`）

全 API エンドポイントへのアクセスを型付きで提供。各機能ごとに namespace で分割:

```typescript
memoriesApi.list({ offset, limit, type, q })
usersApi.get(id)
channelSettingsApi.upsert(channelId, { mode, home })
toolsApi.toggle(name, enabled)
llmApi.update({ preset: "local-qwen" })
schedulerApi.trigger(task)
// etc.
```
