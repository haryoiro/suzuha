# アーキテクチャ精査・再編計画

2026-04-20 策定。PR #111〜#125 系列の capability/behavior 移行を完了した後、**coherent hybrid (Horizontal × Vertical)** として一貫させるための refinement 計画。

正式ルールは `.claude/rules/architecture.md`、skeleton 詳細は `.claude/rules/capability-template.md`、rationale は `architecture-rationale.md`。本書は **何をどの順で変更するか** を記す。

## 1. ゴール

**Horizontal × Vertical の coherent hybrid に到達する**:

- **Vertical (主役)**: ドメインごとの package (memory / conversation / research / action / ...)、flat に `capability/` 配下に配置
- **Horizontal (共有)**: `port/` (契約), `adapter/` (外部実装), `domain/` (共有型+振る舞い), `lib/` (汎用 primitive), `runtime/` (agent loop), `observe/` (cross-cutting), `api/` (HTTP), `channel/` (I/O)
- **capability/behavior の分離廃止**: `capability/` に一本化、port 公開は内部 configuration

### 守る原則 (3 失敗パターン回避)

1. **共通化の誘惑を拒む**: 「他でも使いそう」で早期に lib/horizontal 化しない。**3 回以上の consumer が出てから** 共通化
2. **依存方向は一方向**: depguard で強制、例外なし
3. **粒度揃えすぎない**: capability 内部構造は規模に応じて可変。小さければ平置き、大きければ sub-pkg

## 2. 現状認識

### 完了済み (PR #111〜#125)
- 旧 `internal/feature/*`, `internal/memento`, `internal/memory`, `internal/llm`, `internal/mcp`, `internal/voice` を `capability/` と `behavior/` に再配置
- `port/`, `adapter/`, `domain/`, `runtime/` 層の確立
- `.claude/rules/architecture.md` と `docs/architecture/architecture-rationale.md` の整備

### 残存する問題 (本計画の対象)

| # | 問題 | 現在地 | 本質 |
|---|---|---|---|
| P1 | capability/behavior 分離が identity と usage を混在 | top-level divider | 軸の混在 |
| P2 | `lib/llmtoken`, `lib/llmtext`, `lib/llmtrace` が domain 依存または副作用あり | `lib/` | 共通化の誘惑 |
| P3 | `runtime/scheduler/event/toolregistry` が port 実装 (本来 adapter 相当) | `runtime/` | application と infra の混在 |
| P4 | `domain/` が貧血 (347 行 / method 2 個) | `domain/` | 振る舞いが散在 |
| P5 | capability 内部構造がバラバラ (llm 689 行、vision 平置き 5 ファイル、memory は sub-pkg 化済) | `capability/*/` | 統一テンプレ不在 |
| P6 | vision / voice / mcp に port 未公開 | `capability/` | capability 原則違反 |
| P7 | 1 tool = 1 ファイル違反 (`vision/tools.go` 4 tool 混在、`behavior/action/tools.go` 3 tool 混在) | 複数箇所 | 命名規則違反 |

## 3. Phase 構成

各 Phase は独立して main へ commit 可能。docs 先行 → 大きい構造変更 → 小さい整理 の順。

### Phase A: docs 整備 (先行)

**目的**: 方向性を正式化してから code を動かす。

#### A-1. `.claude/rules/architecture.md` 書き直し
- capability/behavior 分離廃止を明記
- 各 horizontal 層の 置く/置かない 表を追加 (本書 § 4 参照)
- rich domain 方針を反映
- coherent hybrid の原則 + 3 失敗パターン警告

#### A-2. `.claude/rules/capability-template.md` 修正
- 硬直化した「過剰でも sub-pkg」方針を緩和
- 平置きと sub-pkg の使い分け基準を明記
- 具体例 (memory は sub-pkg、builtin は平置き) で示す

#### A-3. `docs/architecture/architecture-rationale.md` trim
- ルール重複部分を削除
- rationale (なぜ Hexagonal、なぜ VSA) と depguard サンプルのみ残す

検証: docs review のみ。コード変更なし。

### Phase B: behavior 廃止・capability 統合 (最大構造変更)

**目的**: top-level 分離を単一軸に統一。

#### B-1. 移動
```
agent/internal/behavior/action/   → agent/internal/capability/action/
agent/internal/behavior/builtin/  → agent/internal/capability/builtin/
agent/internal/behavior/research/ → agent/internal/capability/research/
agent/internal/behavior/video/    → agent/internal/capability/video/
```

`behavior/` ディレクトリ削除。

#### B-2. import path 書き換え
全 internal package で `github.com/.../behavior/<X>` → `github.com/.../capability/<X>`。

#### B-3. depguard rule 更新
- `behavior-siblings` rule 削除
- `capability-siblings` rule を既存範囲 (action, builtin, research, video 追加) で動作確認

#### B-4. DI 配線
`di/provider.go`, `di/adapters.go` の import 更新のみ (配線ロジックは不変)。

検証:
- `go build -buildvcs=false ./agent/...`
- `go test -tags fts5 ./agent/...`
- `scripts/reload.sh` で agent 再起動
- Discord (`chat_id=1484450828302680154`) で既存 tool (research / action / video / builtin) 動作確認

### Phase C: `lib/llm*` 再配置

**目的**: 共通化の誘惑で lib に入り込んだ domain 依存コードを正しい層に戻す。

#### C-1. `lib/llmtoken` → `domain/message/token.go`
- `message.Message` → token 数の計算を Message の method に
- tiktoken wrapper 部分が project 非依存なら `lib/tiktoken/` に分離
- 使用箇所 (`capability/llm`, `runtime/agent`) の import 書き換え

#### C-2. `lib/llmtext` → `domain/message/directive.go`
- `[SKIP]` 等 directive の parsing / 除去を Message の method に
- 使用箇所 (`runtime/agent`) の import 書き換え

#### C-3. `lib/llmtrace` → `observe/` か `adapter/langfuse/`
- Langfuse 送信は **副作用あり**。lib に置いてはいけない (観測可能性関連なので observe/ が自然)
- `observe/langfuse/tracer.go` のような配置案
- 使用箇所の import 書き換え

#### C-4. `lib/llmconv` の判断
- 外部 SDK 型 (`providers.Message`) への変換。suzuha 固有だが domain に置くと SDK 型が domain に入ってしまう
- **判定**: lib 残留 + lib が domain を import する例外を documentation で明示 (または adapter/llm/ に移動して「各 provider adapter が使う変換 helper」にする)

検証: 各 import 変更後に build + test。

### Phase D: `runtime/` 定義明確化

**目的**: application (agent loop) と infrastructure (scheduler/event/toolregistry) の混在を解消。

#### 選択肢
- **D-strict**: infrastructure 部分を `adapter/` に移動
  - `runtime/scheduler/` → `adapter/scheduler/`
  - `runtime/event/` → `adapter/event/` (or observe 寄りなので要検討)
  - `runtime/toolregistry/` → `adapter/toolregistry/`
  - `runtime/gateway/` → `adapter/gateway/` or `runtime/` 残留 (lifecycle 管理は application 寄り)
  - `runtime/agent/` のみ残す (application layer)
- **D-loose**: 現状維持、architecture.md に「runtime は application + agent loop 密接 infrastructure を含む」と明記

**推奨**: D-loose (現状維持 + 定義明確化)。理由:
- scheduler/event/toolregistry は agent loop と密結合 (名前解決や優先度制御が runtime にあり)
- 引っ越しコストが大きい割に原理的改善が小さい
- ただし **runtime/ 内で application と infrastructure を sub-dir で分離** する選択肢あり: `runtime/agent/`, `runtime/infra/{scheduler,event,toolregistry,gateway}/`

### Phase E: domain rich model 移行 (ongoing)

**目的**: 散在ロジックを domain 型の method として集約。

#### 候補 (発見次第追加)
- `domain/memo/similarity.go`: `func ComputeSimilarity(a, b *Memo) float64` (consolidate から移動)
- `domain/memo/merge.go`: `func (m *Memo) CanMergeWith(other *Memo) bool` (consolidate から移動)
- `domain/message/token.go`: token 計算 (Phase C-1 で移動)
- `domain/message/directive.go`: directive (Phase C-2 で移動)
- `domain/message/role.go`: `func (m *Message) IsFromAgent() bool` 等
- `domain/llm/capability.go`: 既存 `ModelInfo.HasCapability` に加え RoleSpec 系の判定

**方針**: 大規模一括移行ではなく、 capability を触る PR で **関連 domain logic を発見した時点で移行** する opportunistic 作業。

### Phase F: capability 内部構造の統一 (標準化)

**目的**: capability-template.md に沿って内部を整理。

#### F-1. 1 tool = 1 ファイル違反の修正
- `capability/vision/tools.go` (4 tool 混在) → `capability/vision/tool/{servo,capture,face,look}.go`
- `capability/action/tools.go` (3 tool 混在) → `capability/action/tool/{create,list,cancel}.go`

#### F-2. port 未公開 capability への port 付与
- `port/vision/` 新設、`capability/vision/Service` を interface 化
- `port/voice/` 新設
- `port/mcp/` 新設
- 各 consumer の import を直接参照から port 経由に変更

#### F-3. 大きすぎる capability の責務分割
- `capability/llm/llm.go` (689 行) → facade + 責務 sub-pkg (role 解決, provider registry, completion 等)
- `capability/llm/provider_registry.go` (561 行) → adapter 側に移すか、capability 内の sub-pkg 化

#### F-4. maintenance task の責務 sub-pkg 化 (既に済み)
memory の acquire/consolidate/forget/summarize は既に sub-pkg 化済 ✓

## 4. 各 horizontal 層の 置く / 置かない (Phase A-1 で architecture.md へ反映)

| 層 | 置くべき | 置かない |
|---|---|---|
| `port/` | interface 定義、契約専用の request/response 型、domain 型への参照 | struct 実装、helper 関数、default 実装 |
| `adapter/` | port 実装 struct、外部 SDK 呼び出し、domain↔SDK mapping、DB schema migration | business logic、domain 型の定義、他 adapter の import |
| `domain/` | Entity、VO、型の method、domain 特化 enum/error | I/O、外部 SDK 型、orchestration、副作用 |
| `lib/` | stateless 関数群、汎用 primitive、project 非依存 wrapper | **domain 型に依存するコード**、副作用、business rule |
| `runtime/` | agent loop、port 実装 (scheduler/event/toolregistry、D-loose 採用時) | capability/behavior の個別知識、business logic |
| `channel/` | Source、Session、Sender、protocol 固有 Tool | business logic、他 channel の import |
| `api/` | HTTP handler (thin)、ogen 生成、middleware | business logic、他 surface (admin↔control) の import |
| `observe/` | metrics/span/log/trace util (副作用 OK) | business logic、domain 型の定義 |
| `di/` | wiring、constructor 呼び出し | business logic、domain 型の定義 |
| `capability/<X>/` | facade、responsibility、store、task、tool、handler (詳細は capability-template.md) | sibling capability の直接 import |

## 5. 順序の根拠

- **docs 先行 (Phase A)**: 方向を固定してから code を動かす。途中で方針がぶれると refactor コストが倍増
- **Phase B が 2 番目**: 最大の構造変更。後続の C/D/E/F は B の結果の上で動く
- **Phase C → E**: lib/llm* を domain に移すことで rich domain 移行の momentum が出る
- **Phase D は独立 (推奨 D-loose で軽微変更)**: 判断保留でも他 Phase に影響しない
- **Phase F は小粒・継続的**: capability ごとに触る機会で随時

## 6. Non-goals

- pure Vertical Slice への転換 (shared infrastructure の重複が増えて逆に悪い)
- pure Hexagonal への転換 (application 層と infrastructure の wrapper 層を増やしても benefit 薄)
- capability/behavior 概念の外部再導入 (flat の coherent hybrid を維持)
- 旧 migration-plan.md の Phase 体系の再利用 (それは完了済み)

## 7. リスク

- Phase B (behavior → capability) で DI 配線を一度に書き換えるので、import エラーを全部潰してから動作確認するまで agent が動かなくなる時間がある
- Phase C で Langfuse tracing が壊れると観測性が失われる → 動作確認必須
- Phase F-2 (vision/voice/mcp の port 化) は interface 抽出の判断に時間がかかる可能性

## 8. 次にやること

1. Phase A-1: `.claude/rules/architecture.md` を本計画の方針で書き直す
2. Phase A-2: `.claude/rules/capability-template.md` を修正 (硬直化緩和)
3. Phase A-3: `docs/architecture/architecture-rationale.md` trim

A-1〜A-3 を先に片付け、B に進む。
