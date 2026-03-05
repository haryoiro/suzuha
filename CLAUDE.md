# suzuha2 開発ガイド

## ビルド環境

このプロジェクトはDockerコンテナ内でビルド・実行されます。ホストマシンにはsqlite3ヘッダやGoのCGO依存が入っていません。

- `go build`, `go test`, `go vet` 等は**ホストで直接実行しないこと** — `docker compose exec agent` 経由で実行する
- `sqlite3` コマンドも同様にコンテナ内で実行する
- ホストで実行可能なのは `go vet ./internal/config/` のようなCGO不要のパッケージのみ

```bash
# ビルド確認
docker compose exec agent go build ./...

# テスト
docker compose exec agent go test ./...

# sqlite3
docker compose exec agent sqlite3 /data/memory.db
```

## Docker構成

- `docker compose up` で agent, admin, searxng, admin-frontend が起動
- agent は Air によるホットリロード対応
- llama.cpp server は別途 `docker run` で起動 (コンテナ名: `llama-qwen3-5`, ポート: 8000)

## LLMプロバイダ切り替え

`config.yaml` の `llm.presets` に名前付きプリセットを定義済み。ランタイムで切り替え可能:

```bash
curl -X PUT http://localhost:9090/internal/llm \
  -H "Content-Type: application/json" \
  -d '{"preset": "local-qwen"}'
```
