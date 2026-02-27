---
applyTo: "*"
paths: "*"
---

# 開発環境

## ローカル環境

ホストマシンに Go はインストールされていない。Go ツールチェーンは Docker コンテナ内のみ。

## Docker Compose

設定ファイル: `docker-compose.yaml`（`.yml` ではない）

### コンテナ一覧

| サービス | イメージ | ポート | 用途 |
|---------|---------|--------|------|
| agent | suzuha-agent (Dockerfile.dev) | 9090 | Discord ボット本体 |
| consolidator | suzuha-consolidator (Dockerfile.dev) | - | 記憶圧縮 (gRPC) |
| admin | suzuha-admin (Dockerfile.dev) | 8080 | 管理画面 API |
| admin-frontend | node:22-slim | 5173 | React SPA (Vite dev server) |
| searxng | searxng/searxng | - | Web 検索 |

### ホットリロード

全 Go サービスは Air によるホットリロード。ソースは `.:/app` でバインドマウント。
コード変更後は `docker compose restart <service>` で Air が再ビルドする。
`docker compose up -d --build` はイメージ再ビルド（依存変更時のみ必要）。

### ビルド・テスト

Go のビルドやテストはコンテナ内で実行する:

```sh
# ビルド確認
docker compose exec -T agent go build ./...

# テスト実行
docker compose exec -T agent go test ./internal/consolidator/ ./internal/affinity/ -v

# 特定パッケージのビルド
docker compose exec -T agent go build ./internal/admin/...
```

### Frontend

TypeScript の型チェックはホストで実行可能（node_modules はホスト側にもある）:

```sh
cd web/admin && npx tsc --noEmit
```

### 再起動

```sh
# 単一サービス
docker compose restart agent

# 複数サービス
docker compose restart agent consolidator

# イメージ再ビルド（go.mod 変更時等）
docker compose up -d --build --force-recreate agent
```

### DB

SQLite ファイルは `data/memory.db`。マイグレーションは goose（起動時に自動適用）。
マイグレーションファイル: `internal/memory/migrations/`
