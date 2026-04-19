# アーキテクチャ設計書：パッケージ配置の目標形

このドキュメントは、現状の `agent/internal/` の構造的な問題を整理し、**抽象度と依存方向に一貫性のあるパッケージ配置** の目標形を定義する。移行はこの設計を基準に段階的に行う（本書では移行計画には踏み込まず、ゴールの定義に専念する）。

---

## 1. 現状の問題

現行構造の 4 つの構造的欠陥：

1. **抽象度のミスマッチ**
   同じ階層 (`internal/` 直下) に、以下のような性質の違うものが横並びになっている：
   - 外部 API クライアント（`llm/`, `mcp/`）
   - ドメインモデル（`memory/`, `user/`）
   - オーケストレータ（`agent/`, `scheduler/`, `gateway/`）
   - 汎用プリミティブ（`lib/`）

   これらは抽象度も変更頻度も依存の向きも違うのに、同格 subsystem として並んでいるため「これらは対等」という構造的な嘘をついている。

2. **実装と契約の癒着**
   `internal/llm/` に OpenAI/Zhipu/Gemini の HTTP クライアントと、`ProviderRegistry` という調整ロジックと、`Client` interface が混在している。バグ調査時に「契約レベルの問題か、SDK 周りの問題か」を package の位置から判別できない。

3. **`external/` の意味不明さ**
   `internal/` は Go 言語の仕様（visibility 制約）だが、`external/` は単なる慣習名で、意味が確定していない。現状 `external/transcript/`, `external/embedding/`, `external/tts/` などが混在し、「外部サービス SDK wrapper」のつもりだが `internal/llm/` も実質同じことをしている。区別基準が曖昧。

4. **`feature/` の癒着**
   `feature/research/` は Task 実装と Tool 実装と純ロジック（HTTP 取得・要約）と DB 操作を 1 package に詰め込んでいる。「research とは何か」を知るのに 1 package を通読するしかなく、Task だけ書き直したい／Tool だけテストしたい、という局所的な変更ができない。

---

## 2. 設計原則

Clean Architecture の層構造は借りないが、**依存方向の一方向性** という中核アイデアだけを採用する。

### 原則 1：抽象度で段を切る

package は必ず以下の 7 段のどれかに属する：

| 段 | 役割 | 変更頻度 |
|---|---|---|
| `lib/` | stdlib に無い汎用プリミティブ | 稀 |
| `domain/` | 純データ（エンティティ・値型） | 稀 |
| `port/` | interface 定義のみ（契約） | 稀 |
| `core/` | pipeline / agent / session 等の調整役 | 中 |
| subsystem（flat） | 純ロジックモジュール（diary, research 等） | 中 |
| `task/` `tool/` `channel/` `admin/` | 1 interface を実装する薄い shim | 高 |
| `driver/` | 外部 SDK / DB を叩く具象実装 | 高 |

同じ段の sibling は抽象度が揃うことを保証する。`llm` と `memory` は同列にならず、**それぞれが port と driver に分割される**。

### 原則 2：契約と実装を物理的に分ける

interface は `port/` に、実装は `driver/` に置く。

```
port/llm/           # interface Client, Message, Tool の定義だけ
driver/llm/openai/  # OpenAI SDK を叩く *openai.Client
driver/llm/zhipu/
driver/llm/gemini/
```

テストが落ちたとき、`port/` が落ちたら契約違反、`driver/` が落ちたらベンダー固有の不具合、と **ディレクトリ名で責任範囲が決まる**。

### 原則 3：依存方向は一方向のみ

```
cmd/                               全てに依存（DI 配線専用）
  ↓
task/ tool/ channel/ admin/        薄い shim（1 interface 実装）
  ↓
<subsystem>/                       純ロジック（diary, research, memento ...）
  ↓
core/                              オーケストレーション
  ↓
port/                              契約
  ↓                                ↑
driver/  ───────────────────────── port を満たす実装（core を知らない）
  ↓
domain/                            純データ
  ↓
lib/                               プリミティブ
  ↓
stdlib
```

**同じ段の sibling 間 import は禁止**。共有したくなったら下の段に降ろす。

### 原則 4：1 package = 1 interface の実装

`task/` `tool/` `channel/` `admin/` の配下に置く package（または .go ファイル）は、**そこに対応する 1 つの interface の実装だけ** を含む。

- `task/diary.go` は `port/scheduler.Task` を実装することに特化した薄い shim
- `tool/web_search.go` は `port/tool.Tool` を実装することに特化した薄い shim
- 100 行を超えたら「実ロジックを subsystem に抽出するサイン」

複数 interface を同居させない。ロジックは subsystem に逃がす。

### 原則 5：subsystem は `*sql.DB` を知らない

`diary/` `research/` などの subsystem package は、**自分が必要とする永続化操作を interface として自身で定義** し、SQL は `driver/store/<name>/` に置く。

```go
// internal/diary/ （subsystem 側）
package diary

type Store interface {
    SaveEntry(ctx context.Context, e Entry) error
    ListEntriesInRange(ctx context.Context, from, to time.Time) ([]Entry, error)
}

func Write(ctx context.Context, s Store, llmCli llm.Client, period Period) error {
    // Store 越しにしか DB を触らない
}
```

```go
// internal/driver/store/diary/ （driver 側）
package diarystore

type SQLite struct { db *sql.DB }
func (s *SQLite) SaveEntry(ctx context.Context, e diary.Entry) error { ... }
```

subsystem は**自分が何を必要とするか**を interface で宣言するだけ。実装の有無は cmd/ の DI が責任を持つ。テストは in-memory の fake Store で完結する。

### 原則 6：`external/` は廃止

すべての「外部サービスを叩くコード」は `driver/` に吸収する。`external/` という名前は完全に消える。

---

## 3. 目標パッケージ配置

```
agent/
├── cmd/
│   ├── suzuha-agent/         # 本体バイナリ
│   ├── suzuha-admin/         # admin サーバ専用バイナリ（必要なら）
│   ├── suzuha-bench/
│   └── suzuha-synth/
│
└── internal/
    ├── lib/                  # L0: stdlib 補完
    │   ├── jtime/
    │   ├── crypto/
    │   └── ...
    │
    ├── domain/               # L1: 純データ
    │   ├── memo/             # Memo, MemoryType, Keywords
    │   ├── user/             # User, Platform
    │   ├── message/          # Message, Role
    │   ├── channel/          # ChannelID, PlatformID, Source kind
    │   └── session/          # SessionID 等の値型
    │
    ├── port/                 # L2: 契約（interface のみ）
    │   ├── llm/              # Client, Request, Response, Tool
    │   ├── scheduler/        # Scheduler, Task
    │   ├── tool/             # Tool
    │   ├── chat/             # Interface, Sender
    │   ├── embedder/         # Embedder
    │   ├── tts/              # Synthesizer
    │   ├── stt/              # Transcriber
    │   ├── vad/              # VoiceActivityDetector
    │   ├── mcp/              # Client, Server
    │   └── transcript/       # VideoTranscriptFetcher
    │
    ├── core/                 # L3: オーケストレーション
    │   ├── agent/            # Agent 本体、ライフサイクル
    │   ├── pipeline/         # Perceive/Think/Act/Reflect
    │   ├── session/          # per-source 実行コンテキスト
    │   ├── gateway/          # Source 登録 hub (errgroup)
    │   ├── scheduler/        # Scheduler 実装（cron runner、Task registry）
    │   ├── tool/             # Tool Registry
    │   ├── event/            # イベントバス
    │   ├── conversation/     # 会話履歴の保持
    │   └── observe/          # Langfuse, metrics, log ring buffer
    │
    ├── memento/              # L3.5 subsystem: 記憶の獲得と統合
    │   ├── acquirer.go       # 純ロジック
    │   ├── consolidator.go
    │   └── store.go          # consumer-side interface
    │
    ├── diary/                # L3.5 subsystem: 日記ロジック
    │   ├── write.go
    │   ├── query.go
    │   └── store.go
    │
    ├── research/             # L3.5 subsystem: 研究ロジック
    │   ├── search.go
    │   ├── fetch.go
    │   ├── summarize.go
    │   └── store.go
    │
    ├── wander/               # L3.5 subsystem
    ├── topics/
    ├── forget/
    ├── video/                # 映像理解
    ├── vision/               # カメラ画像理解
    ├── location/
    ├── voice/                # 音声パイプライン（VAD/STT/TTS 配線ロジック）
    │
    ├── task/                 # L4: port/scheduler.Task の実装（薄い shim）
    │   ├── diary.go          # calls diary.Write
    │   ├── wander.go
    │   ├── research.go
    │   ├── topics.go
    │   ├── forget.go
    │   ├── video.go
    │   ├── vision.go
    │   └── location.go
    │
    ├── tool/                 # L4: port/tool.Tool の実装（薄い shim）
    │   ├── memo.go           # 完結（小さいのでロジック内包）
    │   ├── skip_response.go
    │   ├── web_search.go     # calls research.Search
    │   ├── web_fetch.go
    │   └── ...
    │
    ├── channel/              # L4: port/chat.Source 等の実装
    │   ├── discord/
    │   ├── device/           # ESP32 WebSocket
    │   ├── web/              # Web widget
    │   └── cli/
    │
    ├── admin/                # L4: 管理 API の HTTP handler
    │   ├── server.go
    │   ├── handler/
    │   └── middleware/
    │
    ├── api/                  # ogen 生成コード専用
    │   ├── admin/gen/
    │   └── control/gen/
    │
    ├── di/                   # 全 package 横断の DI 配線
    │
    └── driver/               # L5: 具象実装
        ├── llm/
        │   ├── openai/
        │   ├── zhipu/
        │   └── gemini/
        ├── store/            # subsystem 別の SQL 実装を並べる
        │   ├── memento/
        │   ├── diary/
        │   ├── research/
        │   ├── user/
        │   └── ...
        ├── embedder/
        │   ├── gemini/
        │   └── openai/
        ├── tts/
        │   ├── voicevox/
        │   └── sbv2/
        ├── stt/
        │   ├── whisper/
        │   └── ...
        ├── mcp/
        ├── transcript/       # 旧 external/transcript
        ├── twitter/
        └── discord/          # discordgo wrapper（channel/discord/ から使う）
```

### subsystem をどう識別するか

`internal/` 直下にあって、**`lib/` `domain/` `port/` `core/` `task/` `tool/` `channel/` `admin/` `api/` `di/` `driver/` のどれでもないもの** が subsystem。flat に並べる。

subsystem の候補：`memento` `diary` `research` `wander` `topics` `forget` `video` `vision` `location` `voice`。

---

## 4. 依存ルール詳細

### 4.1 許可される import 方向

| from → to | 許可 | 備考 |
|---|---|---|
| `cmd/` → 任意 | ✓ | DI 配線のため |
| `task/X.go` → `<subsystem>/` | ✓ | shim → ロジック |
| `task/X.go` → `port/` `domain/` `core/` | ✓ | interface 実装に必要な参照 |
| `tool/X.go` → `<subsystem>/` | ✓ | shim → ロジック |
| `tool/X.go` → `port/` `domain/` `core/` | ✓ | 同上 |
| `channel/X/` → `core/` `port/` `domain/` | ✓ | Source/Session 実装 |
| `admin/` → `<subsystem>/` `core/` `port/` | ✓ | 管理 API |
| `<subsystem>/` → `port/` | ✓ | 契約経由で外界にアクセス |
| `<subsystem>/` → `domain/` | ✓ | 値型の利用 |
| `<subsystem>/` → `core/` | △ | **原則避ける**。core の一部が必要なら port に切り出す |
| `core/` → `port/` | ✓ | 契約のみ依存 |
| `core/` → `domain/` | ✓ | 値型の利用 |
| `port/` → `domain/` | ✓ | interface 引数型に domain を使う |
| `driver/X/` → `port/` | ✓ | 実装すべき契約 |
| `driver/X/` → `<subsystem>/` | ✓（型定義のみ） | 例: `driver/store/diary/` が `diary.Entry` を借りる |
| `driver/X/` → `domain/` `lib/` | ✓ | |

### 4.2 禁止される import 方向

| from → to | 禁止 | 理由 |
|---|---|---|
| **任意の同段 sibling 間** | ✕ | subsystem 同士・task 間・tool 間・channel 間・driver 間の横断禁止 |
| `driver/` → `core/` | ✕ | driver は port 越しに呼ばれる |
| `driver/` → `task/` `tool/` `channel/` `admin/` | ✕ | 層逆行 |
| `port/` → `core/` `driver/` `<subsystem>/` | ✕ | 契約は実装を知らない |
| `core/` → `task/` `tool/` `channel/` `admin/` `<subsystem>/` | ✕ | core はプラグインを知らない |
| `core/` → `driver/` | ✕ | driver は cmd/ で DI 経由で注入される |
| `<subsystem>/` → `task/` `tool/` `channel/` `admin/` `driver/` | ✕ | 層逆行 |
| `domain/` → 任意の他 package | ✕（`lib/` 除く） | 純データ |
| `lib/` → 任意の他内部 package | ✕ | プリミティブ |

### 4.3 強制手段

- `depguard` lint rule で静的に検知
- CI で `go vet` + カスタム linter を走らせる
- 違反をコメントでなくビルドエラーに落とす

---

## 5. subsystem package の書き方

### 5.1 構造

```
internal/<subsystem>/
├── <name>.go         # 公開関数・型（Entry, Period 等 + Write, Query 等）
├── store.go          # このsubsystem が必要とする Store interface
├── <internal>.go     # 内部ヘルパー
└── <name>_test.go    # fake Store での単体テスト
```

### 5.2 ルール

1. **`*sql.DB` / `database/sql` を直接 import しない**
2. **必要な永続化操作は package 内で `type Store interface { ... }` として定義**
3. **外部 API は `port/` 経由で呼ぶ**（LLM は `port/llm.Client`、TTS は `port/tts.Synthesizer` など）
4. **sibling subsystem を import しない**。共有したい型があれば `domain/` に昇格、共有したい処理があれば `core/` か下位 port に降ろす
5. **1 subsystem しか使わない interface はその subsystem 内に置く**。複数 subsystem が共有する interface のみ `port/` に昇格
6. **テストは fake/stub Store と fake LLM で完結**する設計にする

### 5.3 shim の書き方

`task/` `tool/` `admin/` に置く shim は：

- 50 行以下が目安、100 行を超えたら subsystem 抽出を検討
- interface の義務を果たすだけ：引数のパース、subsystem 関数呼び出し、結果の成形
- ビジネスロジックを直接書かない

```go
// internal/task/diary.go （50 行以下の shim の例）
package task

type Diary struct {
    store  diary.Store
    llm    llm.Client
    logger *slog.Logger
}

func NewDiary(s diary.Store, c llm.Client, l *slog.Logger) *Diary {
    return &Diary{store: s, llm: c, logger: l}
}

func (d *Diary) Name() string { return "diary" }

func (d *Diary) Run(ctx context.Context) error {
    period := diary.CurrentPeriod(time.Now())
    return diary.Write(ctx, d.store, d.llm, period)
}
```

---

## 6. 配置判定フロー（新しい概念の置き場を決める）

```
新しい package を作りたい
  │
  ├─ 外部サービス/DB を叩く具体実装か？
  │     → YES → driver/<kind>/<vendor>/
  │
  ├─ 外界との新しい対話プロトコルか？
  │     → YES → channel/<protocol>/
  │
  ├─ port/scheduler.Task を実装する？
  │     → YES → task/<name>.go（ロジックは subsystem に）
  │
  ├─ port/tool.Tool を実装する？
  │     → YES → tool/<name>.go（ロジックは subsystem に）
  │
  ├─ 管理画面経由の操作（HTTP handler）か？
  │     → YES → admin/handler/<area>.go
  │
  ├─ pipeline / session / scheduler 自体の変更か？
  │     → YES → core/<subsystem>/（慎重に）
  │
  ├─ 複数 subsystem が共有する interface か？
  │     → YES → port/<contract>/
  │
  ├─ 純粋なエンティティ/値型か？
  │     → YES → domain/<entity>/
  │
  ├─ ロジックモジュール（Task/Tool からも admin からも呼べる）か？
  │     → YES → internal/<name>/ （flat な subsystem）
  │
  └─ 汎用的なプリミティブか？
        → YES → lib/<name>/
```

「どれにも当てはまらない」が頻発するなら、その時点で設計書を見直す。

---

## 7. 廃止・統合対象

| 現在 | 目標 | 備考 |
|---|---|---|
| `external/` | 廃止 | 内容はすべて `driver/` へ |
| `internal/adapter/` | 廃止 | 内容は `channel/` へ |
| `internal/feature/` | **廃止** | ロジックは `internal/<name>/` に、Task shim は `task/<name>.go` に |
| `internal/memento/acquirer/` `internal/memento/consolidator/` | `memento/` にフラット化 | SQL は `driver/store/memento/` に分離 |
| `internal/llm/` の interface + 実装 | `port/llm/` + `driver/llm/<vendor>/` | ProviderRegistry は `core/` or `port/llm/` 配下 |
| `internal/memory/` の Store | `port/store/memento/` + `driver/store/memento/` | |
| `internal/lib/` | `lib/`（位置同じ、役割明確化） | L0 として明示 |

---

## 8. 命名ルール

- ディレクトリ名 = 段 / 役割を表す言葉
- package 名 = ディレクトリ名と一致（Go 慣習）
- interface は consumer 側（subsystem 内 or port/ 内）に置き、**動詞＋er（`Synthesizer`, `Transcriber`, `Embedder`）か、ドメイン名そのまま（`Store`, `Client`）**
- driver サブディレクトリ名 = ベンダー・技術名（`openai`, `sqlite`, `voicevox`）
- shim のファイル名 = subsystem 名と一致（`task/diary.go` ⇄ `diary/`）
- `utils/`, `helpers/`, `common/`, `misc/` は禁止（置き場の決まらない package は設計ミス）

---

## 9. このドキュメントのスコープ外

- 現状から目標形への **移行手順** は別ドキュメント（`migration-plan.md`）で扱う
- `core/pipeline/` 内部の細かいファイル配置（Perceive/Think/Act/Reflect の分け方）
- リント設定の具体ルールは `.golangci.yml` と `depguard` 設定で管理
