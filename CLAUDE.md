# suzuha 開発ガイド

## リポジトリ構成

Go workspace (`go.work`) を使用したモノレポ:

- `agent/` — Go モジュール (`cmd/`, `internal/`, `external/`)
- `spec/` — TypeSpec API 定義 + 生成 OpenAPI
- `web/` — フロントエンド (admin, widget)
- `firmware/` — ESP32 ファームウェア
- `container/` — Docker 設定

## ビルド・テスト

CGO依存あり。**ホストで直接実行しない** — 必ず Docker 経由:

```bash
docker compose -f container/compose.yaml exec agent go build -buildvcs=false -tags fts5 ./agent/...
docker compose -f container/compose.yaml exec agent go test -tags fts5 ./agent/...
```

## コミット

lefthook + commitlint で強制: `type(scope): 日本語の説明` (header 72文字以内)

## LLM 切り替え

```bash
curl -X PUT http://localhost:9090/internal/llm/roles/conversation \
  -H "Content-Type: application/json" -d '{"provider":"zhipu","model":"glm-4.7"}'
```

## 自己改善

git worktree で作業 → PR 作成 → **絶対にマージしない** (オーナーがレビュー)

詳細: `.claude/rules/` にアーキテクチャ規約あり
