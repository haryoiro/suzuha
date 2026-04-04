---
description: テスト規約
paths:
  - "**/*_test.go"
---

## テストスタイル

- table-driven + `t.Run` サブテスト
- ヘルパーには `t.Helper()` を付ける
- teardown は `t.Cleanup()` (`defer` ではない)
- パッケージ内テスト (`package xxx`) が基本

## このプロジェクト固有

- テスト実行はコンテナ内: `docker compose -f container/compose.yaml exec agent go test -tags fts5 ./...`
- CGO依存パッケージはホストでテストできない
