---
description: capability/<X>/ の標準 skeleton と可変な sub-package 戦略
---

## 方針

**規模に応じた可変な skeleton**。小さい capability は平置き、大きい capability は sub-package。硬直的な "全員同じ skeleton" は避ける (architecture.md § 3 失敗パターン #3)。

`architecture.md` の補足。位置づけ・依存ルール・層規約はそちらを参照。

## 3 段階の capability skeleton

### 小規模 (A): 平置き

責務が 1 つ、tool が 2-3 個以下、store の schema / query が素朴なとき。

```
capability/<name>/
  <name>.go             # facade + 主要ロジック (最大 ~300 行まで許容)
  <verb>.go             # 純ロジック (必要に応じて分割)
  store.go              # (永続化ある場合) interface
  postgres.go           # (永続化ある場合) PostgreSQL 実装
  task.go               # (scheduler task ある場合)
  tool_<verb>.go        # 1 tool = 1 ファイル (2-3 個なら平置き)
  handler.go            # (HTTP ある場合)
  <name>_test.go
```

実例 (現状): `capability/builtin/` (tool_python.go + tool_user_profile.go)、`capability/research/` (tool_fetch.go + research.go 等)

### 中規模 (B): 部分 sub-package

tool が 4 個以上、または単一責務内で module 化したい要素があるとき。

```
capability/<name>/
  <name>.go             # facade
  <verb>.go             # 純ロジック
  store.go              # interface
  postgres.go           # PostgreSQL 実装
  task.go               # scheduler task
  tool/                 # tool 4 個以上なら sub-pkg
    <verb1>.go
    <verb2>.go
    <verb3>.go
    <verb4>.go
  <name>_test.go
```

実例: `capability/vision/` (4 tool → `tool/` sub-pkg に集約する予定)、`capability/action/` (3 tool → 数個ずつ増えれば sub-pkg へ)

### 大規模 (C): responsibility + concern sub-package

責務が 3 個以上 (acquire / consolidate / forget 等)、かつ各責務がそれ自体で閉じたロジックを持つとき。

```
capability/<name>/
  <name>.go             # facade (DI 集約、delegate のみ、50-100 行)

  <resp1>/              # responsibility sub-pkg
    <resp1>.go
    <verb>.go
    store.go            # 責務固有 store (ある場合)
    task.go             # 責務固有 task (ある場合)
    <resp1>_test.go

  <resp2>/
    ...

  store/                # capability 共通 store (schema / query helper が複雑なとき)
    store.go
    postgres.go
    queries.go

  task/                 # 独立 task が複数あるとき
    <task1>.go
    <task2>.go

  tool/                 # tool 4 個以上のとき
    <verb1>.go
    <verb2>.go

  handler/              # HTTP handler が複雑なとき
    handler.go

  <name>_test.go
```

実例: `capability/memory/` (acquire / consolidate / forget / summarize の 4 責務 sub-pkg)

## 段階選定の基準

| 状況 | skeleton |
|---|---|
| 責務 1 個、tool ≤ 3、store 単純 | A (平置き) |
| 責務 1-2 個、tool ≥ 4 or store 複数実装 | B (部分 sub-pkg) |
| 責務 3 個以上、独立 workflow 内包 | C (responsibility sub-pkg) |

**判断**: 「このファイル 1 つを開いて、責務が **1 つに集中** しているか?」YES なら現段階維持。複数責務が混在し始めたら次段階へ。

**予防的分割はしない**。3 回以上の concern が具体化してから sub-pkg を切る。

## 各 slot の責務

### `<name>.go` (facade)
- 小規模: `type Service struct`、`New`、主要 public method、純ロジック (300 行まで許容)
- 中規模以上: DI エントリポイント + sub-package への薄い delegate のみ (50-100 行)
- 他 package はここ経由でのみアクセス (sibling 経由 API は `port/<name>/` に切る)

### `<verb>.go` (純ロジック)
- I/O 非依存の計算・変換・判定
- store / 外部 SDK / runtime に触れない
- 発見したら domain/<X>/ に昇格できないか検討 (rich domain 原則)

### `store.go` / `<storage>.go`
- `store.go` は consumer-side interface (当該 capability が必要とする operation)
- `postgres.go` が PostgreSQL 実装 (suzuha の永続化は PostgreSQL 一本)
- 単体テスト用の fake store は in-memory で書く (`_test.go` 内 or test helper)
- `database/sql` (正確には pgx) を直接触るのはここだけ
- schema migration は `adapter/store/<name>/migrations/` へ

### `task.go` / `task/<task>.go`
- `port/scheduler.Task` 実装 shim
- **shim は薄い** (< 50 行目安): Run() で当該 capability の method を呼ぶだけ
- ロジックは <verb>.go や responsibility sub-pkg に置く
- task が 2 個以上 → `task/` sub-pkg

### `tool_<verb>.go` / `tool/<verb>.go`
- `port/tool.Tool` 実装 shim
- 1 tool = 1 ファイル、ファイル名で tool 名を表現
- `func NewXxxTool(...) port/tool.Tool` factory (concrete 露出禁止)
- DI で tool registry に register
- tool が 4 個以上 → `tool/` sub-pkg

### `handler.go` / `handler/*.go`
- HTTP handler (chi / ogen)
- capability method を呼ぶ薄い wrapper (request/response 変換のみ)
- ロジックは書かない

### 責務 sub-package (`<resp>/`)
- 「LLM 判断を含む 1 つのオペレーション」で切る
- 例: `memory/acquire` (抽出) / `memory/consolidate` (統合) / `memory/forget` (忘却) / `memory/summarize` (要約)
- sub-pkg 内に store / task / tool が閉じるなら sub-pkg 内へ
- sub-pkg 同士の直接 import は避ける (facade 越し、または port 越し)

## port 分割

`port/<name>/` に 1 本の巨大 interface を置かない。consumer の用途で分ける (3-5 メソッド程度):

```
port/memory/
  memory.go       # Memory (agent pipeline が使う主機能)
  management.go   # Management (admin CRUD)
  media.go        # Media (media 添付管理)
  store.go        # 内部 store 契約 (adapter が実装)
```

判断基準: consumer が「何をしたいか」で切る。小さい interface を複数 >> 巨大 interface 1 個。

## 命名

| ファイル名 | 役割 |
|---|---|
| `<name>.go` | facade (package と同名、1 ファイル) |
| `<verb>.go` | 純ロジック (`search.go` / `write.go` 等、I/O 触らない) |
| `store.go` | store interface |
| `postgres.go` | store 実装 (PostgreSQL) |
| `task.go` or `task/<task>.go` | scheduler task shim |
| `tool_<verb>.go` or `tool/<verb>.go` | LLM tool shim |
| `handler.go` or `handler/<area>.go` | HTTP handler |
| `<file>_test.go` | テスト |

**混在禁止**: 同一 capability 内で `tool_*.go` と `tool/<verb>.go` を併用しない (片方に統一)。`task.go` 単独と `task/` sub-pkg は併用しない。

## 禁止事項

- facade `<name>.go` を 300 行以上に肥大化させる (責務分割タイミング)
- 1 ファイルに複数 tool / 複数 task
- sub-pkg 同士が循環 import する設計
- `utils` / `helpers` / `common` / `misc` / `base` / `shared` sub-pkg
- `init()` 関数 / グローバル可変状態
- 予防的 sub-pkg (「将来使いそう」で切る)

## 例

### capability/memory (C - 大規模、現状で sub-pkg 済)

```
capability/memory/
  memory.go                  # Service facade
  acquire/
    acquire.go               # LLM 抽出
    store.go
    task.go
  consolidate/
    consolidate.go           # 類似 memo 統合
    task.go
  forget/
    forget.go
    task.go
  summarize/
    summarize.go             # 会話要約
    daily.go
    hourly.go
    task.go
  store/                     # 共通 store (責務横断)
    store.go
  tool/
    memo.go                  # memo create tool

port/memory/
  memory.go                  # Memory interface
  acquire.go
  consolidate.go
  forget.go
  summarize.go
  store.go
```

### capability/builtin (A - 小規模、平置き)

```
capability/builtin/
  builtin.go                 # facade (DI 集約のみ、30 行)
  tool_python.go             # python exec tool
  tool_user_profile.go       # user profile tool
  builtin_test.go
```

port なし。tool registry 経由で runtime から触れるのみ。

### capability/vision (B - 中規模、tool sub-pkg 候補)

現状は平置きだが tool が 4 個あるので sub-pkg 化候補:

```
capability/vision/
  vision.go                  # facade
  tracker.go                 # トラッキング実装
  stream.go                  # ストリーム
  change.go
  pipeline.go
  yolo.go
  tool/
    servo.go                 # 4 tool を sub-pkg に
    capture.go
    face.go
    look.go
  vision_test.go
```

## capability 昇格・格下げのタイミング

### A → B に昇格
- tool 数が 4 を超えた
- store の schema / query が複雑化して分割したい

### B → C に昇格
- 独立した workflow (LLM 判断で 1 つのオペレーション完結) が 3 個以上になった
- facade が 100 行を超える

### 格下げ (B → A, C → B)
- 基本的には **しない**。構造を上げるのは容易だが下げるのは呼び出し側への影響が大きい
- 例外: 責務が廃止されて sub-pkg が空になった場合は削除

## architecture.md との関係

- architecture.md § capability 内部構造の可変性 の要約 = 本書 § 3 段階
- architecture.md § 配置判定 = 本書 § 段階選定の基準
- architecture.md § Never = 本書 § 禁止事項
- 依存ルール・層規約は architecture.md 参照
