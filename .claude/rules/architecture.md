---
description: レイヤー構成、依存方向、パッケージ配置のルール（target-layout.md 要約）
---

## 目標アーキテクチャ

Hexagonal (Ports & Adapters) × Vertical Slice のハイブリッド。詳細は `docs/architecture/target-layout.md`。

## 二群分離

| 区分 | 定義 | port | 実例 |
|---|---|---|---|
| **capability**（能力） | agent が **持つ** 能力。他から呼ばれる。maintenance task も含む | 必ず `port/<X>/` 公開 | memory, llm, voice, vision, conversation, mcp |
| **behavior**（行動） | agent の **自律行動**（LLM を使う） | 持たない（shim が `port/scheduler.Task` / `port/tool.Tool` 実装） | research, action, video |

迷ったら capability（port を切っておけば後で behavior 化は容易。逆は困難）。

## 依存方向（上→下のみ、同段 sibling 禁止）

```
cmd/                         （composition root）
  ↓
api/, channel/, adapter/, behavior/
  ↓
capability/  ⇄  port/        （capability は port を実装、他コードは port 越しに呼ぶ）
  ↓
runtime/                     （agent loop 本体、個別 package を知らない）
  ↓
domain/                      （Entity / Value Object）
  ↓
lib/                         （stdlib 補完プリミティブ）
  ↓
stdlib
```

- `runtime/` は capability / behavior を知らない
- `adapter/` は `port/` と `domain/`, `lib/`, `observe/` のみ import 可
- `port/` は純契約 — 実装を一切知らない（capability/behavior/adapter/runtime 禁止）
- 同種パッケージ間（capability 同士、behavior 同士、channel 同士、adapter 同士）の直接 import は禁止
- 共有は `domain/` (型) / `port/` (契約) / `lib/` (プリミティブ) 経由

## パッケージ配置

Go コードは `agent/` ディレクトリ配下:

| 種類 | 配置先 |
|---|---|
| 能力 (port 公開) | `agent/internal/capability/{name}/` |
| 自律行動 | `agent/internal/behavior/{name}/` |
| 入出力プロトコル | `agent/internal/channel/{name}/` |
| 契約 (interface のみ) | `agent/internal/port/{name}/` |
| 外部 SDK / 永続化実装 | `agent/internal/adapter/{name}/` or `agent/internal/adapter/{category}/{vendor}/` |
| HTTP サーフェス | `agent/internal/api/{admin,control}/` |
| ドメイン型 | `agent/internal/domain/{name}/` |
| agent loop 本体 | `agent/internal/runtime/{agent,pipeline,session,gateway,scheduler,toolregistry,conversation,event}/` |
| DI 配線 | `agent/internal/di/` |
| ユーティリティ | `agent/internal/lib/{name}/` |
| metric/trace/log util | `agent/internal/observe/` （層ルール対象外） |

## ファイル命名規則（capability / behavior 内）

**1 ファイル = 1 責務**:

| 役割 | ファイル名 |
|---|---|
| 公開 API | `<name>.go` |
| 純ロジック | `<verb>.go`（`search.go` / `write.go` 等、I/O 触らない） |
| 永続化契約 | `store.go`（consumer-side interface） |
| 永続化実装 | `<storage>.go`（`postgres.go` / `sqlite.go`） |
| scheduler Task | `task.go`（shim、50 行以下） |
| LLM Tool | `tool_<verb>.go`（**1 tool = 1 ファイル**） |
| HTTP handler | `handler.go` |
| テスト | `<file>_test.go` |

## 判断ルール

### 新しい型を置く場所
「別 package の import 文に書きたくなるか？」YES なら `domain/<name>/`。

### 共有ロジック
1. 汎用プリミティブ → `lib/<name>/`
2. agent loop と絡む → `runtime/<name>/`
3. 2 つ以上の behavior で使う → 新規 `internal/<name>/` subsystem

予防的には切らない。**2 package 以上が import したくなってから**。

## Never

- adapter が他の adapter を import
- capability / behavior 同士を直接 import（`port/` 経由で）
- domain が adapter / external を import
- port が実装（capability / behavior / adapter / runtime）を import
- `utils` / `helpers` / `common` / `misc` / `base` / `shared` package 名
- `init()` 関数 / グローバル可変状態

## 強制

- `.golangci.yml` の depguard rule で静的検知
- CI で `go vet` + `golangci-lint run` → 違反はビルドエラー
- `scripts/update-depguard-baseline.sh` で Phase 毎に違反数を snapshot、増減を確認

## 移行中の暫定ルール

現行 `internal/feature/` は Phase 8d で `internal/behavior/` にリネーム予定。それまでは `feature-siblings` ルール（feature 同士 import 禁止）が適用される。
