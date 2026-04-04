# suzuha2 開発ガイド

## ビルド環境

このプロジェクトはDockerコンテナ内でビルド・実行されます。ホストマシンにはsqlite3ヘッダやGoのCGO依存が入っていません。

- `go build`, `go test`, `go vet` 等は**ホストで直接実行しないこと** — `docker compose exec agent` 経由で実行する
- `sqlite3` コマンドも同様にコンテナ内で実行する
- ホストで実行可能なのは `go vet ./internal/config/` のようなCGO不要のパッケージのみ

```bash
# ビルド確認
docker compose -f container/compose.yaml exec agent go build ./...

# テスト
docker compose -f container/compose.yaml exec agent go test ./...

# sqlite3
docker compose -f container/compose.yaml exec agent sqlite3 /data/memory.db
```

## Docker構成

- `docker compose -f container/compose.yaml up` で agent, admin, searxng, admin-frontend が起動
- agent は Air によるホットリロード対応
- llama.cpp server は別途 `docker run` で起動 (コンテナ名: `llama-qwen3-5`, ポート: 8000)

## LLMプロバイダ切り替え

`config.yaml` の `llm.presets` に名前付きプリセットを定義済み。ランタイムで切り替え可能:

```bash
curl -X PUT http://localhost:9090/internal/llm \
  -H "Content-Type: application/json" \
  -d '{"preset": "local-qwen"}'
```

## 自己改善ワークフロー (Self-Improve)

suzuha2 が Discord の `#self-improve` チャンネル (ID: `1484450828302680154`) に改善リクエストを投稿する。
Claude Code (Discord plugin 経由) がリクエストを受け取り、コード変更を行う。

### 手順

1. suzuha2 からのリクエストが `#self-improve` チャンネルに届く
2. **git worktree** を使い、Air の監視外で作業する:
   ```bash
   git worktree add /tmp/suzuha-wt-<name> -b self-improve/<name>
   ```
3. worktree 内でコードを変更・テスト:
   ```bash
   cd /tmp/suzuha-wt-<name>
   # コード編集...
   docker compose -f container/compose.yaml exec agent go build -buildvcs=false -tags fts5 ./...
   ```
4. 変更をコミット:
   ```bash
   git add -A && git commit -m "self-improve: <内容>"
   ```
5. worktree を削除 (ブランチは残す):
   ```bash
   git worktree remove /tmp/suzuha-wt-<name>
   ```
6. チャンネルにブランチ名と変更内容を報告
7. PR を作成:
   ```bash
   git push origin self-improve/<name>
   gh pr create --base main --head self-improve/<name> \
     --title "self-improve: <内容>" \
     --body "suzuha2からの自己改善リクエスト"
   ```
8. **絶対にマージしないこと** — オーナーがレビュー後にマージする

### 重要
- Air は `cmd/suzuha-agent/` と `internal/` の `.go` / `.yaml` を監視している
- worktree (`/tmp/`) での変更は再起動を引き起こさない
- `docker compose -f container/compose.yaml exec agent go build` でビルド確認すること (ホストではCGOが使えない)
