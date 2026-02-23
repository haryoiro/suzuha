# suzuha セットアップガイド

## 前提条件

- Go 1.23+
- SQLite3（FTS5, sqlite-vec 拡張付き）
- Discord Bot Token（Discord で利用する場合）
- LLM API Key（OpenAI 互換プロバイダー）

## 1. Discord Bot Token の取得

### 1.1 アプリケーション作成

1. [Discord Developer Portal](https://discord.com/developers/applications) にアクセス
2. 右上の **「New Application」** をクリック
3. アプリケーション名を入力（例: `suzuha`）して **「Create」**

### 1.2 Bot の有効化

1. 左メニューの **「Bot」** を選択
2. **「Reset Token」** をクリックしてトークンを生成
3. 表示されたトークンをコピーして安全な場所に保存（**一度しか表示されない**）

> ⚠️ トークンは絶対に公開しないでください。漏洩した場合は即座に「Reset Token」で再生成してください。

### 1.3 Privileged Gateway Intents の有効化

同じ Bot 設定ページで、以下の Intent を **すべて ON** にする:

| Intent | 用途 |
|--------|------|
| **Presence Intent** | ユーザーのオンライン状態を検知 |
| **Server Members Intent** | サーバーメンバー情報へのアクセス |
| **Message Content Intent** | メッセージ内容の読み取り（**必須**） |

### 1.4 Bot をサーバーに招待

1. 左メニューの **「OAuth2」** → **「URL Generator」** を選択
2. **Scopes** で `bot` にチェック
3. **Bot Permissions** で以下にチェック:
   - Send Messages
   - Read Message History
   - Add Reactions
   - Use Slash Commands（将来用）
4. 生成された URL をブラウザで開き、招待先サーバーを選択して **「認証」**

### 1.5 Bot ID の確認

1. 左メニューの **「General Information」** を選択
2. **Application ID** をコピー — これが `bot_id` になる

## 2. LLM API Key の取得

suzuha は OpenAI 互換 API を使用する。以下のいずれかのプロバイダーを利用可能:

| プロバイダー | 取得先 | config の `provider` |
|-------------|--------|---------------------|
| OpenAI | https://platform.openai.com/api-keys | `openai` |
| ZhiPu（智谱） | https://open.bigmodel.cn/ | `zhipu` |
| その他 OpenAI 互換 | 各サービスのダッシュボード | `openai` + `api_base` 指定 |

### Embedding モデル

ベクトル検索に Embedding モデルが必要。推奨:

| プロバイダー | モデル | 次元数 |
|-------------|--------|--------|
| OpenAI | `text-embedding-3-small` | 1024 |
| ZhiPu | `embedding-3` | 1024 |

## 3. 設定ファイル

`config.yaml` をプロジェクトルートに作成:

```yaml
llm:
  provider: "openai"          # openai, zhipu
  model: "gpt-4o"
  api_key: ""                 # 環境変数 LLM_API_KEY でも可
  api_base: ""                # OpenAI 互換の場合はエンドポイントを指定
  max_tokens: 128000
  embedding_model: "text-embedding-3-small"
  embedding_dims: 1024

discord:
  token: ""                   # 環境変数 DISCORD_TOKEN でも可
  bot_id: "123456789012345678"

memory:
  db_path: "data/memory.db"

agent:
  prompt_dir: "prompts"       # IDENTITY.md, SOUL.md を配置
  context_window_pct: 0.8     # 80% でコンテキスト圧縮
  interest_threshold: 0.5

consolidator:
  address: "localhost:50051"
  agent_notify: "localhost:50052"
  scheduler:
    enabled: false
    jobs: []

observe:
  log_level: "info"           # debug, info, warn, error
  metrics_addr: ":9090"

admin:
  addr: ":8080"
```

### 環境変数

機密情報は環境変数で渡すことを推奨:

```bash
export LLM_API_KEY="sk-..."
export DISCORD_TOKEN="MTIzNDU2..."
```

`.env` ファイルは `.gitignore` に含まれているため、ローカルに置いても安全。

## 4. プロンプトファイル

`prompts/` ディレクトリに以下のファイルを配置:

### `IDENTITY.md` — キャラクター設定

```markdown
あなたは suzuha です。
フレンドリーで好奇心旺盛な性格を持つ AI エージェントです。
```

### `SOUL.md` — 行動指針

```markdown
## 基本方針
- 自然な会話を心がける
- 質問には丁寧に答える
- 面白い話題には積極的に参加する
```

## 5. ビルドと起動

### ビルド

```bash
go build -o suzuha-agent ./cmd/suzuha-agent
go build -o suzuha-consolidator ./cmd/suzuha-consolidator
go build -o suzuha-admin ./cmd/suzuha-admin
```

### 起動（3プロセス構成）

```bash
# ターミナル 1: Consolidator（メモリ圧縮 + スケジューラ）
./suzuha-consolidator

# ターミナル 2: Agent（メインプロセス）
./suzuha-agent

# ターミナル 3: Admin（管理画面、任意）
./suzuha-admin
```

### 開発モード（air によるホットリロード）

```bash
# Agent
air -c .air.agent.toml

# Consolidator
air -c .air.consolidator.toml
```

### CLI モード（Discord なし）

Discord トークンを設定しなければ自動的に CLI モードで起動する:

```bash
LLM_API_KEY="sk-..." ./suzuha-agent
```

## 6. RSS フィード設定

スケジューラを有効にして RSS 監視を開始する:

```yaml
consolidator:
  scheduler:
    enabled: true
    jobs:
      - name: "rss-check"
        task: "rss"
        cron: "*/30 * * * *"       # 30分ごと
        config:
          vector_threshold: 0.3    # ベクトル類似度の候補閾値
          notify_threshold: 0.6    # LLM スコアの通知閾値
          max_articles_per_notify: 5
```

フィードの登録は Discord 上で suzuha に話しかけるか、Agent のツール経由で行う:

```
ユーザー: このRSS登録して https://go.dev/blog/feed.atom
suzuha:  (rss_subscribe ツールで登録)
```

## 7. 動作確認

### ヘルスチェック

```bash
# メトリクス
curl http://localhost:9090/metrics

# コンテキスト状態
curl http://localhost:9090/internal/context

# 管理画面
open http://localhost:8080
```

### ログ確認

```bash
# リアルタイムログ（SSE）
curl -N http://localhost:9090/internal/logs
```

## 8. トラブルシューティング

| 症状 | 原因 | 対策 |
|------|------|------|
| `no such module: fts5` | SQLite に FTS5 が含まれていない | CGO 有効でビルド、または FTS5 対応の SQLite を使用 |
| `consolidator connection failed` | Consolidator が未起動 | 先に `suzuha-consolidator` を起動 |
| Bot がメッセージに反応しない | Message Content Intent が OFF | Developer Portal で Intent を有効化 |
| `empty embedding response` | Embedding モデル未設定 | `embedding_model` を設定 |
| `notification: agent rejected` | Agent の通知サーバーが未起動 | `agent_notify` アドレスを確認 |
