# 設定と構成

## 設定ファイル

メインの設定は `config.yaml`（gitignored）。環境変数 `SUZUHA_CONFIG` でパスを上書き可能。

## 設定構造

```yaml
timezone: "Asia/Tokyo"  # IANA タイムゾーン

llm:
  provider: "openai"        # "openai", "zhipu", "qwen" 等
  model: "gpt-4o"
  api_key: ""               # 環境変数 LLM_API_KEY で上書き可
  api_base: ""              # カスタムエンドポイント
  max_tokens: 8000          # コンテキストウィンドウサイズ
  presets:                  # ランタイム切り替え用プリセット
    - name: "local-qwen"
      provider: "openai"
      model: "qwen3-5"
      api_base: "http://host.docker.internal:8000/v1"
      max_tokens: 32768
      vision: false
    - name: "cloud-gpt4o"
      provider: "openai"
      model: "gpt-4o"
      max_tokens: 128000
      vision: true

embedding:
  provider: ""              # 未指定時は llm.provider を継承
  model: "text-embedding-3-small"
  api_key: ""               # 環境変数 EMBEDDING_API_KEY
  api_base: ""
  dims: 1024                # ベクトル次元数

vision:
  provider: ""              # 未指定時は llm.provider を継承
  model: "glm-4.6v-flash"
  api_key: ""               # 環境変数 VISION_API_KEY
  api_base: ""

discord:
  token: ""                 # 環境変数 DISCORD_TOKEN
  bot_id: ""

voice:
  enabled: false
  whisper_url: "http://whisper:8001"
  voicevox_url: "http://voicevox:50021"
  speaker_id: 3             # zundamon normal
  allowed_channels: []      # 空 = 全チャンネル許可

tool_servers:               # MCP ツールサーバー（静的定義）
  - name: "example"
    type: "mcp"
    transport: "stdio"
    command: "/path/to/server"
    args: ["--arg"]
    env:
      KEY: "value"

triggers: []                # プロアクティブトリガー（未使用）

memory:
  db_path: "memory.db"      # SQLite データベースパス

agent:
  prompt_dir: ".suzuha"     # IDENTITY.md, SOUL.md の格納ディレクトリ
  interest_threshold: 0.5
  context_window_pct: 0.8   # この使用率で圧縮トリガー

consolidator:
  api_addr: ":9091"
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
          channel_id: ""    # 未指定時は home チャンネルを自動検出
      - name: "RSS巡回"
        task: rss
        cron: "@every 30m"
      - name: "ウェブ探索"
        task: explore
        cron: "@every 2h"
        config:
          searxng_url: "http://searxng:8080"
          max_depth: 3
      - name: "好感度更新"
        task: affinity
        cron: "@every 6h"
      - name: "記憶整理"
        task: forget
        cron: "0 4 * * *"   # 毎日 4:00

observe:
  log_level: "info"         # "debug", "info", "warn", "error"
  metrics_addr: ":9090"

admin:
  addr: ":8080"
  agent_metrics: "http://localhost:9090/metrics"
  agent_logs: "http://localhost:9090/internal/logs"
  agent_context: "http://localhost:9090/internal/context"
  consolidator_api: "http://localhost:9091"
  static_dir: "web/admin/dist"
  prompt_dir: ".suzuha"
  auth:
    username: ""            # 未指定時は認証なし
    password: ""

location:
  enabled: false
  token: ""                 # 環境変数 OVERLAND_TOKEN
```

## 環境変数

| 変数 | 用途 |
|------|------|
| `SUZUHA_CONFIG` | config.yaml のパス |
| `LLM_API_KEY` | LLM API キー |
| `EMBEDDING_API_KEY` | 埋め込み API キー |
| `VISION_API_KEY` | ビジョン API キー |
| `DISCORD_TOKEN` | Discord Bot トークン |
| `OVERLAND_TOKEN` | Overland GPS トラッキングトークン |
| `YOLO_URL` | YOLO サーバー URL（デフォルト: `http://yolo:8002`） |

## プロンプトファイル

`agent.prompt_dir`（デフォルト: `.suzuha/`）に配置:

| ファイル | 役割 |
|---------|------|
| `IDENTITY.md` | エージェントのアイデンティティ（名前、性格、基本設定） |
| `SOUL.md` | 行動指針、価値観、口調の詳細 |

起動時に `IDENTITY.md` + `SOUL.md` を結合して `Agent.SystemPrompt` に設定。Admin UI の Prompts ページから編集・リロード可能。

## LLM プロバイダー切り替え

### プリセット方式

```bash
curl -X PUT http://localhost:9090/internal/llm \
  -H "Content-Type: application/json" \
  -d '{"preset": "local-qwen"}'
```

### カスタム指定

```bash
curl -X PUT http://localhost:9090/internal/llm \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "openai",
    "model": "gpt-4o-mini",
    "api_base": "https://api.openai.com/v1",
    "max_ctx": 128000,
    "vision": true
  }'
```

切り替え時の動作:
1. LLM クライアントのプロバイダーを変更
2. コンテキストの `maxTokens` を更新
3. 使用率 > 50% なら即座にコンテキスト圧縮
4. 設定を `app_settings` テーブルに永続化（再起動後も維持）

## DI コンテナ

`samber/do/v2` を使用。`cmd/suzuha-agent/providers.go` で全プロバイダーを登録:

```go
func allPackages(cfgPath string) []func(do.Injector) {
    return []func(do.Injector){
        agentPackages(cfgPath),  // クロスカッティング
        config.Package,
        observe.Package,
        event.Package,
        tool.Package,
        memory.Package,
        llm.Package,
        mcp.Package,
        consolidator.Package,
        user.Package,
        channel.Package,
    }
}
```

各パッケージは `Package` 関数で自身のプロバイダーを登録。循環依存を避けるため、`agentPackages` でブリッジ（例: `memory.EmbedFunc → llm.Client.Embed`）。

## Docker 構成

### サービス一覧

| サービス | ポート | 説明 | プロファイル |
|---------|--------|------|-------------|
| `agent` | 9090, 8080 | メインエージェント | (デフォルト) |
| `admin-frontend` | 5173 | Vite 開発サーバー | (デフォルト) |
| `searxng` | - | メタ検索エンジン | (デフォルト) |
| `yolo` | - | YOLO 物体検出 | (デフォルト) |
| `whisper` | - | Whisper STT (CUDA) | `voice` |
| `voicevox` | 50021 | VOICEVOX TTS (CUDA) | `voice` |
| `suzuha1` | 9091, 8082 | 2 体目のエージェント | `suzuha1` |
| `firmware-esp32` | - | ESP32 ファームウェアビルド | `firmware` |
| `firmware-p4` | - | ESP32-P4 ファームウェアビルド | `firmware` |

### ボリューム

- `go-mod-cache`, `go-build-cache`: Go ビルドキャッシュ
- `node-modules`: フロントエンド依存
- `npm-cache`: npm キャッシュ
- `./data:/data`: SQLite DB、動的ツール等の永続データ
- `./.suzuha:/app/.suzuha`: プロンプトファイル
