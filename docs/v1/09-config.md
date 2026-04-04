# 設定と構成

## 設定ファイル

メインの設定は `config.yaml`（gitignored）。環境変数 `SUZUHA_CONFIG` でパスを上書き可能。

## 設定構造

```yaml
timezone: "Asia/Tokyo"  # IANA タイムゾーン

llm:
  providers:                # プロバイダ定義 (接続情報)
    - name: "openai"
      type: "openai"
      api_key: ""           # 環境変数 LLM_API_KEY で上書き可
      api_base: "https://api.openai.com/v1"
    - name: "local"
      type: "openai"
      api_base: "http://host.docker.internal:8000/v1"
    - name: "zhipu"
      type: "openai"
      api_key: ""
      api_base: "https://open.bigmodel.cn/api/paas/v4"

embedding:
  provider: ""              # 未指定時は llm.provider を継承
  model: "text-embedding-3-small"
  api_key: ""               # 環境変数 EMBEDDING_API_KEY
  api_base: ""
  dims: 1024                # ベクトル次元数

discord:
  token: ""                 # 環境変数 DISCORD_TOKEN
  bot_id: ""

voice:
  enabled: false
  stt:                        # STT プロバイダー (優先度順)
    - provider: "deepgram"
      api_key: ""             # 環境変数 DEEPGRAM_API_KEY
      model: "nova-3"
    - provider: "whispercpp"
      url: "http://whisper:8001"
  tts:                        # TTS プロバイダー (優先度順)
    - provider: "voicevox"
      url: "http://voicevox:50021"
      speaker_id: 3
    - provider: "sbv2"
      url: "http://sbv2:5000"
  allowed_channels: []        # 空 = 全チャンネル許可

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

consolidator:               # 旧名。scheduler 設定のみ使用
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
      - name: "ウェブ探索"
        task: explore
        cron: "@every 2h"
        config:
          searxng_url: "http://searxng:8080"
          max_depth: 3
      - name: "記憶整理"
        task: forget
        cron: "0 4 * * *"   # 毎日 4:00

observe:
  log_level: "info"         # "debug", "info", "warn", "error"
  internal_addr: ":9090"

admin:
  addr: ":8080"
  agent_metrics: "http://localhost:9090/metrics"
  agent_logs: "http://localhost:9090/internal/logs"
  agent_context: "http://localhost:9090/internal/context"
  # consolidator_api は廃止 (インプロセスに移行済み)
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
| `SUZUHA_ENCRYPTION_KEY` | API キー暗号化用マスターキー (hex 64文字 = 32byte) |
| `LLM_API_KEY` | LLM API キー |
| `EMBEDDING_API_KEY` | 埋め込み API キー |
| `VISION_API_KEY` | ビジョン API キー |
| `DISCORD_TOKEN` | Discord Bot トークン |
| `DEEPGRAM_API_KEY` | Deepgram STT API キー |
| `OVERLAND_TOKEN` | Overland GPS トラッキングトークン |
| `YOLO_URL` | YOLO サーバー URL（デフォルト: `http://yolo:8002`） |

## プロンプトファイル

`agent.prompt_dir`（デフォルト: `.suzuha/`）に配置:

| ファイル | 役割 |
|---------|------|
| `IDENTITY.md` | エージェントのアイデンティティ（名前、性格、基本設定） |
| `SOUL.md` | 行動指針、価値観、口調の詳細 |

起動時に `IDENTITY.md` + `SOUL.md` を結合して `Agent.SystemPrompt` に設定。Admin UI の Prompts ページから編集・リロード可能。

## LLM プロバイダー管理

ProviderRegistry で 3 レイヤー（Provider / Model Catalog / Role Assignment）を管理する。
Provider は config.yaml で定義、Model Catalog と Role Assignment は DB で管理。
詳細は [11-llm.md](./11-llm.md) を参照。

### ロール割り当て切り替え

```bash
# conversation ロールを切り替え
curl -X PUT http://localhost:9090/internal/llm/roles/conversation \
  -H "Content-Type: application/json" \
  -d '{"provider": "local", "model": "qwen3-5"}'
```

### プロバイダ・モデル管理

```bash
# プロバイダ一覧
curl http://localhost:9090/internal/llm/providers

# モデルカタログ一覧
curl http://localhost:9090/internal/llm/models
```

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
        memento.Package,
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
| `firmware-esp32` | - | ESP32 ファームウェアビルド | `firmware` |
| `firmware-p4` | - | ESP32-P4 ファームウェアビルド | `firmware` |

### ボリューム

- `go-mod-cache`, `go-build-cache`: Go ビルドキャッシュ
- `node-modules`: フロントエンド依存
- `npm-cache`: npm キャッシュ
- `./data:/data`: SQLite DB、動的ツール等の永続データ
- `./.suzuha:/app/.suzuha`: プロンプトファイル
