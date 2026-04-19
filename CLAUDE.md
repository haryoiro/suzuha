# suzuha 開発ガイド

## リポジトリ構成

Go workspace (`go.work`) + pnpm workspace (`pnpm-workspace.yaml`) のモノレポ:

- `agent/` — Go モジュール (`cmd/`, `internal/`, `external/`)
- `spec/` — TypeSpec API 定義 (`routes/`, `models/`, `docs/`, `generated/`)
- `admin/` — 管理画面 (React SPA, Vite + Ant Design)
- `widget/` — 音声ウィジェット (React + WebSocket)
- `firmware/` — ESP32 ファームウェア
- `container/` — Docker 設定

## ビルド・テスト

**ホストで直接実行しない** — 必ず Docker 経由:

```bash
docker compose -f container/compose.yaml exec agent go build -buildvcs=false ./agent/...
docker compose -f container/compose.yaml exec agent go test ./agent/...
```


## API 生成

```bash
mise run spec   # pnpm --filter api compile → go generate ./... (ogen)
```

spec の書き方は `spec/docs/typespec-rules.md` と `naming-conventions.md` を参照。

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
