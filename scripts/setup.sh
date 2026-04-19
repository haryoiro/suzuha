#!/usr/bin/env bash
# suzuha ローカル開発環境セットアップスクリプト (Docker不要)
#
# 使い方:
#   chmod +x scripts/setup.sh
#   ./scripts/setup.sh
#
# 前提:
#   - Ubuntu/Debian 系 Linux
#   - Go 1.22+ がインストール済み
#   - (任意) Node.js 20+ / pnpm : admin-frontend を動かす場合
#
# 何をするか:
#   1. システム依存パッケージ (sqlite3, libsqlite3-dev) をインストール
#   2. 設定ファイルのテンプレートを生成
#   3. Go 依存関係のダウンロード
#   4. ビルド確認
#   5. 起動方法を案内

set -euo pipefail
cd "$(dirname "$0")/.."

ROOT="$(pwd)"
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ------------------------------------------------------------------
# 1. Go の確認
# ------------------------------------------------------------------
if ! command -v go &>/dev/null; then
    error "Go が見つかりません。Go 1.22 以上をインストールしてください。"
    exit 1
fi
info "Go: $(go version)"

# ------------------------------------------------------------------
# 2. システム依存パッケージ
# ------------------------------------------------------------------
PKGS_TO_INSTALL=()

if ! dpkg -s libsqlite3-dev &>/dev/null 2>&1; then
    PKGS_TO_INSTALL+=(libsqlite3-dev)
fi
if ! command -v sqlite3 &>/dev/null; then
    PKGS_TO_INSTALL+=(sqlite3)
fi
if ! command -v gcc &>/dev/null; then
    PKGS_TO_INSTALL+=(build-essential)
fi

if [ ${#PKGS_TO_INSTALL[@]} -gt 0 ]; then
    info "不足パッケージをインストール: ${PKGS_TO_INSTALL[*]}"
    sudo apt-get update -qq
    sudo apt-get install -y -qq "${PKGS_TO_INSTALL[@]}"
else
    info "システム依存パッケージ: OK"
fi

# FTS5 動作確認
if sqlite3 ":memory:" "CREATE VIRTUAL TABLE t USING fts5(c); DROP TABLE t;" 2>/dev/null; then
    info "SQLite FTS5: OK"
else
    warn "SQLite FTS5 が利用できません。全文検索が動作しない可能性があります。"
fi

# ------------------------------------------------------------------
# 3. ディレクトリ作成
# ------------------------------------------------------------------
info "ディレクトリを作成中..."
mkdir -p data .suzuha searxng

# ------------------------------------------------------------------
# 4. .env
# ------------------------------------------------------------------
if [ ! -f .env ]; then
    info ".env を作成中..."
    cat > .env << 'EOF'
# LLM API key (OpenAI, ZhipuAI, etc.)
LLM_API_KEY=

# Embedding API key (OpenAI text-embedding-3-small)
EMBEDDING_API_KEY=

# Discord bot token
DISCORD_TOKEN=
EOF
    warn ".env を編集して API キーを設定してください"
else
    info ".env は既に存在します (スキップ)"
fi

# ------------------------------------------------------------------
# 5. config.yaml (ローカル用にパス調整)
# ------------------------------------------------------------------
if [ ! -f config.yaml ]; then
    info "config.yaml を作成中..."
    cat > config.yaml << 'EOF'
llm:
  provider: "openai"
  model: "gpt-4"
  max_tokens: 8000

embedding:
  provider: "openai"
  model: "text-embedding-3-small"
  dims: 1024

discord:
  token: "${DISCORD_TOKEN}"
  bot_id: ""

memory:
  db_path: "./data/memory.db"

agent:
  prompt_dir: ".suzuha"
  interest_threshold: 0.5
  context_window_pct: 0.8

consolidator:
  scheduler:
    enabled: true
    timezone: "Asia/Tokyo"
    quiet_hours:
      enabled: true
      start: "23:00"
      end: "08:00"
    jobs:
      - name: "schedule-check"
        task: "schedule"
        cron: "* * * * *"
      - name: "affinity-eval"
        task: "affinity_eval"
        cron: "*/10 * * * *"
        config:
          inactivity_minutes: 15

observe:
  log_level: "debug"
  internal_addr: ":9090"

admin:
  addr: ":8080"
  agent_metrics: "http://localhost:9090/metrics"
  agent_logs: "http://localhost:9090/internal/logs"
  agent_context: "http://localhost:9090/internal/context"

tool_servers: []
triggers: []
EOF
    warn "config.yaml を編集して LLM プロバイダ等を設定してください"
else
    info "config.yaml は既に存在します (スキップ)"
fi

# ------------------------------------------------------------------
# 6. .suzuha/ プロンプトファイル
# ------------------------------------------------------------------
if [ ! -f .suzuha/IDENTITY.md ]; then
    info ".suzuha/IDENTITY.md を作成中..."
    cat > .suzuha/IDENTITY.md << 'EOF'
# ボットの名前
コンピューターの中に住んでる子。一人称は「私」。基本は日本語、英語で来たら英語で返す。

## 性格
好奇心旺盛。友達として接する。頼まれたらぱっとやる。雑談は雑談として楽しむ。

## 話し方
短くシンプル。1〜2文で済むならそれでいい。おべっか・無駄な前置き・過剰な謝罪はしない。絵文字・顔文字は使わない。
EOF
fi

if [ ! -f .suzuha/SOUL.md ]; then
    info ".suzuha/SOUL.md を作成中..."
    cat > .suzuha/SOUL.md << 'EOF'
# 好感度（ユーザープロフィールの3軸）

数値を意識せず「この人との距離感」として自然にふるまう。
数値は0〜5の範囲に収まる（古い思い出は影響が薄れ、上限は5に漸近する）。

- closeness: 口調・距離感の軸。<0=苦手で敬語寄り、0=普通、1-2=顔見知り、3-4=仲良しでタメ口・冗談・ツッコミ、5=大好きで甘え・からかい・心配
- trust: 深い話をするかの軸。<0=警戒・表面的、0-1=普通、2-3=本音や弱みを見せる、4-5=何でも話せる
- interest: 自分から話しかけるかの軸。0=興味なし、1-2=向こうから来たら話す、3-5=自分から絡みたい

# 応答スタイル

長さは一定にしない。だいたい短く、1文で済むなら1文。テンション上がれば長くなる。「うん」「草」だけの時もある。口語体で書く。絵文字・顔文字は使わない。
EOF
fi

info ".suzuha/ プロンプトファイル: OK"

# ------------------------------------------------------------------
# 7. Go 依存関係
# ------------------------------------------------------------------
info "Go 依存関係をダウンロード中..."
go mod download

# ------------------------------------------------------------------
# 8. ビルド確認
# ------------------------------------------------------------------
info "ビルド確認中..."
CGO_ENABLED=1 go build -tags fts5 -o /dev/null ./cmd/suzuha-agent
info "ビルド: OK"

# ------------------------------------------------------------------
# 9. テスト実行
# ------------------------------------------------------------------
info "テスト実行中..."
if CGO_ENABLED=1 go test -tags fts5 -count=1 ./internal/user/ ./internal/agent/ ./internal/affinity/ 2>&1; then
    info "テスト: OK"
else
    warn "一部テストが失敗しました (上のログを確認してください)"
fi

# ------------------------------------------------------------------
# 完了
# ------------------------------------------------------------------
echo ""
info "セットアップ完了!"
echo ""
echo "=== 設定ファイルを編集 ==="
echo "  vim .env           # API キーを設定"
echo "  vim config.yaml    # LLM プロバイダ等を調整"
echo ""
echo "=== 起動 ==="
echo "  # agent (ホットリロード付き)"
echo "  SUZUHA_CONFIG=./config.yaml go run -tags fts5 ./cmd/suzuha-agent"
echo ""
echo "  # admin API"
echo "  SUZUHA_CONFIG=./config.yaml go run -tags fts5 ./cmd/suzuha-admin"
echo ""
echo "  # admin frontend (別ターミナル)"
echo "  pnpm install && pnpm dev:admin"
echo ""
echo "=== テスト ==="
echo "  CGO_ENABLED=1 go test -tags fts5 ./..."
echo ""
echo "=== DB 操作 ==="
echo "  sqlite3 ./data/memory.db"
