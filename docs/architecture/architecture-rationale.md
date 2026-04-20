# アーキテクチャ Rationale

## 0. このドキュメントは何か

正式ルールは **`.claude/rules/architecture.md`** (層/依存方向/配置/命名/rich domain/import 表) と **`.claude/rules/capability-template.md`** (sub-package skeleton) に移行済み。

本書は以下を保持する:

- 採用アーキテクチャの選定理由 (なぜ Hexagonal、なぜ Vertical Slice、なぜ UseCase 層を採らないか)
- テスタビリティ原則
- depguard 設定サンプル

設計の原点は `refactor/phase-*` 一連の PR で段階移行された。移行計画の履歴は `migration-plan.md`。

---

## 1. 採用アーキテクチャの rationale

**Hexagonal Architecture (Ports & Adapters / Cockburn 2005)** × **Vertical Slice Architecture (Bogard 2017)** のハイブリッド。

### 採用した要素

- **port / adapter** — Hexagonal の語彙をそのまま採用。契約と実装の分離
- **1 directory = 1 概念** — Vertical Slice の発想。「X を直す」は 1 ディレクトリで完結
- **flat capability/ (coherent hybrid)** — 業務 domain slice を top-level で 1 層 flat に、横断技術層 (port/adapter/domain/lib/runtime/observe/api/channel) と直交
- **DDD Entity / Value Object** — domain の型定義で借用 (rich domain、貧血禁止)

### 採用しない要素

- **capability / behavior の top-level 分離** — identity (domain 概念) と usage (port 公開) を 1 軸に混ぜる設計だった。2026-04 以降は廃止、`capability/` に統合し port 公開は内部 configuration として扱う
- **DDD Aggregate / Domain Event** — メンテナンスコストに見合わない
- **DDD bounded context の wrapper dir 化** — Java/C# mindset。Go では flat + 横断層の直接並置が idiomatic
- **Clean Architecture の 4 層・UseCase 層** — port / adapter + capability で責務分離が足りる。UseCase 層追加は ceremony 過多
- **Functional Core / Imperative Shell** — Go + LLM 中心の I/O には合わない (非同期処理・streaming・error recovery の組み合わせが pure core に入れづらい)
- **pure Vertical Slice** — suzuha は shared infrastructure (LLM / embedder / TTS / STT) 重なりが多く、slice 完全独立が成立しない

### 設計の根拠

- **軸の分離**: top-level で横断技術層 (horizontal) と業務 slice (vertical) を明示的に分ける
- **規模可変の内部構造**: 硬直な skeleton を押し付けない。小さい capability は平置き、大きい capability は sub-pkg (`capability-template.md` 参照)
- **rich domain** (貧血禁止) で型の振る舞いを domain に寄せる → 散在する純ロジックを防ぐ
- **共通化の誘惑を拒む** (`architecture.md` § 3 失敗パターン #1): 1-2 consumer では共通化しない、3 回以上 consumer が出てから

---

## 2. テスタビリティ原則

capability / behavior は以下を満たす設計にする:

- **fake Store** (in-memory 実装) で単体テストが通る
- **fake LLM / fake Embedder** で外部依存なしにテストが書ける
- **runtime に依存しない** ロジックは pure 関数として書き、Session 等は引数で受け取る

テスト容易性は port パターン採用の最大の見返り。テストが書きにくいコードは設計の赤信号。

rich domain 型のメソッドは I/O 非依存なので単体テストがそのまま書ける。domain test は `<name>_test.go` をドメインパッケージ内に並置する。

---

## 3. depguard 設定サンプル

`.golangci.yml` の depguard rule で architecture.md の Import 表を **strict モード** で強制する。baseline exemption なし、違反はビルドエラー。

sibling 禁止を許可リスト展開で書くと爆発するので、**deny + allow 組み合わせ** で書く:

```yaml
rules:
  # capability/X は 他 capability/Y を直接 import 禁止、port/ 経由のみ
  capability-siblings:
    files:
      - "**/internal/capability/*/**"
    deny:
      - pkg: "github.com/haryoiro/suzuha/agent/internal/capability/**"
        desc: "capability 同士の直接 import は禁止。port/ 経由で使うこと"

  # behavior/X は 他 behavior/Y を直接 import 禁止
  behavior-siblings:
    files:
      - "**/internal/behavior/*/**"
    deny:
      - pkg: "github.com/haryoiro/suzuha/agent/internal/behavior/**"
        desc: "behavior 同士の直接 import は禁止"
      - pkg: "github.com/haryoiro/suzuha/agent/internal/capability/**"
        desc: "behavior → capability は port/ 経由のみ"

  # adapter は port, domain, lib のみ
  adapter-only-port:
    files:
      - "**/internal/adapter/**"
    allow:
      - "github.com/haryoiro/suzuha/agent/internal/port/**"
      - "github.com/haryoiro/suzuha/agent/internal/domain/**"
      - "github.com/haryoiro/suzuha/agent/internal/lib/**"
      - "github.com/haryoiro/suzuha/agent/internal/observe/**"
    deny:
      - pkg: "github.com/haryoiro/suzuha/agent/internal/**"
        desc: "adapter は port/domain/lib/observe 以外の internal を import できない"

  # port は実装を知らない
  port-is-pure:
    files:
      - "**/internal/port/**"
    deny:
      - pkg: "github.com/haryoiro/suzuha/agent/internal/capability/**"
      - pkg: "github.com/haryoiro/suzuha/agent/internal/behavior/**"
      - pkg: "github.com/haryoiro/suzuha/agent/internal/runtime/**"
      - pkg: "github.com/haryoiro/suzuha/agent/internal/adapter/**"
```

### 運用ポイント

- `files` でルール適用ディレクトリを絞る
- `deny` を先に書いて禁止対象を宣言、必要なら例外的に `allow` で許す形 (capability 自身の package は Go 的に同 package なら OK)
- 全内部パッケージの列挙は避ける (メンテ不能)
- `scripts/update-depguard-baseline.sh` は出力 0 行を期待、非空は回帰として扱う
- 新パッケージ / 境界を追加するときは depguard rules も同 PR で更新
