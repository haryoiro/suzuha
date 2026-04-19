# アーキテクチャ設計書：パッケージ配置の目標形

## 0. このドキュメントは何か

現状の `agent/internal/` を、**1 feature = 1 directory（ファイル内で役割分離）＋ 横断的な Ports & Adapters** のハイブリッド構造に置き換える。本書はその最終形（ゴール）の定義。

設計の根拠：

- feature-based の凝集度を取る → 「X を直す」は 1 ディレクトリで完結
- Ports & Adapters の契約／実装分離を取る → LLM・DB・TTS のような横断能力だけは port と adapter に切る
- サイズで構造を変えない → 小さい feature も大きい feature も同じファイル命名規則

---

## 1. 採用する architecture

**Hexagonal Architecture (Ports & Adapters / Cockburn 2005)** × **Vertical Slice Architecture (Bogard 2017)** のハイブリッド。

- port / adapter は Hexagonal の語彙をそのまま採用
- feature の区切りは Vertical Slice の発想
- DDD 由来の Entity / Value Object だけ借りる（Aggregate / Domain Event は使わない）
- Clean Architecture の 4 層・UseCase 層は **採用しない**
- Functional Core / Imperative Shell は **採用しない**（Go + LLM 中心の I/O には合わない）

---

## 2. 設計原則

### 原則 1：1 feature = 1 directory

各 feature は `internal/feature/<name>/` に全てのコードを集める。サイズに関係なく同じ構造。

### 原則 2：ファイル単位で役割を分離

feature 内で **1 ファイル = 1 責務**。癒着の原因は「ディレクトリに何でも入れる」ではなく「1 ファイルに複数責務を入れる」。

| 役割 | ファイル名 | 中身 |
|---|---|---|
| 公開 API | `<feature>.go` | 公開型・公開関数 |
| 純ロジック | `<verb>.go` | `search.go` / `write.go` / `consolidate.go` 等。I/O を直接触らない |
| 永続化契約 | `store.go` | Store interface（consumer-side） |
| 永続化実装 | `<storage>.go` | `postgres.go` / `sqlite.go` など |
| scheduler Task | `task.go` | `port/scheduler.Task` 実装 shim |
| LLM Tool | `tool_<name>.go` | `port/tool.Tool` 実装 shim |
| HTTP handler | `handler.go` | admin 経由の操作（必要なら） |
| テスト | `<file>_test.go` | fake Store / fake LLM で完結 |

### 原則 3：cross-cutting は `port/` + `adapter/`

複数 feature で共有される能力（LLM / embedder / TTS / STT / VAD / 字幕取得）は feature 内に置かず、`port/` に契約、`adapter/` に実装を置く。

`scheduler.Task` / `tool.Tool` のような interface 自体も `port/` に集約する（shim の実装は各 feature 内の `task.go` / `tool_*.go`）。

### 原則 4：依存方向は一方向

```
cmd/                                     （DI、全部見える）
  ↓
adapter/                                 （port を実装）
  │
  │    port/                             （契約）
  │      ↑
  ├─── feature/<X>/                      （個別機能、uniform 構造）
  │      │
  │      ↓（公開 API のみ）
  ├─── 他 feature/<Y>/                   （feature 間は最小限）
  │
  ├── channel/                           （入出力プロトコル、feature 相当の扱い）
  ├── admin/                             （HTTP 管理サーフェス）
  │
  ↓
runtime/                                 （agent loop）
  ↓
domain/                                  （Entity / Value Object）
  ↓
lib/                                     （stdlib 補完）
  ↓
stdlib
```

- **runtime は feature を知らない**（runtime は agent loop のみ、個別機能は port 越しに呼ぶ）
- **adapter は port だけを知る**（feature や runtime を import しない）
- **port は純契約**（実装側を一切知らない）

### 原則 5：feature 間の直接 import は最小限

`feature/A/` が `feature/B/` を直接 import するのは原則避ける。

- 型だけ共有するなら → `domain/` に昇格
- interface を共有するなら → `port/` に昇格
- ロジックを共有するなら → 片方に寄せるか、新 feature に切り出す

どうしても必要なら **相手 feature の公開 API**（`<feature>.go` で export された型・関数）**のみ**を import 可。相手の `store.go` `task.go` `tool_*.go` を import してはならない。

### 原則 6：feature 内で `database/sql` を直接使わない

`store.go` で interface を定義、`<storage>.go` がその実装。logic ファイルは Store interface だけを受け取る。これにより：

- fake Store でのユニットテストが書ける
- DB バックエンド差し替えが 1 ファイル内で済む

### 原則 7：禁止 package 名

`utils/`, `helpers/`, `common/`, `misc/`, `base/`, `shared/`。置き場が決まらない package は設計ミス。

---

## 3. 目標ディレクトリ

```
agent/
├── cmd/
│   ├── suzuha-agent/
│   ├── suzuha-admin/
│   ├── suzuha-bench/
│   └── suzuha-synth/
│
└── internal/
    ├── lib/                      # stdlib 補完
    │   ├── jtime/
    │   ├── crypto/
    │   └── ...
    │
    ├── observe/                  # cross-cutting util（framework 対象外、誰でも import 可）
    │   └── （metrics / tracing / log ring buffer）
    │
    ├── domain/                   # 全 feature で共有される Entity / Value Object
    │   ├── memo/                 # Memo, MemoryType, Keywords
    │   ├── user/                 # User, Platform
    │   ├── message/              # Message, Role
    │   └── channel/              # ChannelID, PlatformID, Source kind
    │
    ├── runtime/                  # agent loop そのもの
    │   ├── agent/                # オーケストレータ・ライフサイクル
    │   ├── pipeline/             # Perceive / Think / Act / Reflect
    │   ├── session/              # per-source 実行コンテキスト
    │   ├── gateway/              # Source 登録 hub (errgroup)
    │   ├── scheduler/            # cron runner + Task registry
    │   ├── toolregistry/         # Tool の登録と解決
    │   ├── conversation/         # 会話履歴バッファ
    │   └── event/                # イベントバス
    │
    ├── feature/                  # 1 dir = 1 feature（全員同じファイル構造）
    │   ├── memory/               # 記憶（旧 memento + memory）
    │   ├── llm/                  # LLM プロバイダ管理
    │   ├── diary/                # 日記
    │   ├── research/             # 研究行動
    │   ├── wander/               # 徘徊
    │   ├── voice/                # VAD/STT/TTS 配線
    │   ├── vision/               # カメラ映像理解
    │   ├── location/             # GPS 取り込み + query
    │   ├── mcp/                  # MCP クライアント管理
    │   ├── forget/               # 記憶忘却（極小 feature）
    │   ├── topics/               # 話題の暇度
    │   ├── video/                # 動画理解 tool 群
    │   └── action/               # 予約アクション実行
    │
    ├── channel/                  # 入出力プロトコル（feature 相当の扱い）
    │   ├── discord/
    │   ├── device/               # ESP32 WebSocket
    │   ├── web/                  # Web widget
    │   └── cli/
    │
    ├── api/                      # HTTP サーフェス（入力 adapter）＋ ogen 生成コード
    │   ├── admin/                # データ閲覧・編集（memory/diary/user/channel 等の CRUD）
    │   │   ├── server.go
    │   │   ├── gen/              # ogen 生成（対象外）
    │   │   ├── handler.go        # Handler の配線
    │   │   ├── handler_<resource>.go  # リソース別 handler（feature を呼ぶ薄い shim）
    │   │   ├── middleware/
    │   │   └── store.go          # admin 固有 Store interface（必要なら）
    │   │
    │   └── control/              # ランタイム操作・設定（runtime/scheduler/llm/voicevox/tools 等）
    │       ├── server.go
    │       ├── gen/              # ogen 生成（対象外）
    │       ├── handler.go
    │       └── handler_<area>.go
    │
    ├── port/                     # cross-cutting 契約（interface のみ）
    │   ├── scheduler/            # Task
    │   ├── tool/                 # Tool
    │   ├── chat/                 # Sender
    │   ├── llm/                  # Client（feature/llm の公開 API）
    │   ├── memory/               # Memory（feature/memory の公開 API）
    │   ├── mcp/                  # Client（feature/mcp の公開 API）
    │   ├── embedder/             # Embedder（feature 無し、薄い port）
    │   ├── tts/                  # Synthesizer
    │   ├── stt/                  # Transcriber
    │   ├── vad/                  # VoiceActivityDetector
    │   └── transcript/           # VideoTranscriptFetcher
    │
    ├── adapter/                  # cross-cutting 実装
    │   ├── llm/
    │   │   ├── openai/
    │   │   ├── zhipu/
    │   │   └── gemini/
    │   ├── embedder/
    │   │   ├── gemini/
    │   │   └── openai/
    │   ├── tts/
    │   │   ├── voicevox/
    │   │   └── sbv2/
    │   ├── stt/
    │   │   └── whisper/
    │   ├── vad/
    │   ├── transcript/
    │   └── twitter/
    │
    └── di/                       # composition root
```

---

## 4. feature 内部の構造

### 4.1 標準形（中規模 feature、例：`feature/diary/`）

```
feature/diary/
├── diary.go              # 公開型（Entry, Period）
├── write.go              # 純ロジック：日記を書く
├── query.go              # 純ロジック：期間で取得
├── store.go              # interface Store { SaveEntry, ListEntriesInRange, ... }
├── postgres.go           # SQL 実装（database/sql はここだけ）
├── task.go               # scheduler.Task 実装 shim
└── diary_test.go         # fake Store + fake LLM でテスト
```

コード例：

```go
// feature/diary/diary.go
package diary

type Entry struct {
    ID          string
    Kind        string
    Content     string
    PeriodStart time.Time
    PeriodEnd   time.Time
}

type Period struct{ Start, End time.Time }

// 公開 API。他 feature はここだけ触れる
func CurrentPeriod(now time.Time) Period { ... }
```

```go
// feature/diary/write.go — 純ロジック、I/O なし
func Write(ctx context.Context, s Store, llmCli llm.Client, p Period) error {
    existing, err := s.ListEntriesInRange(ctx, p.Start, p.End)
    // ...
    return s.SaveEntry(ctx, Entry{...})
}
```

```go
// feature/diary/store.go — consumer-side interface
type Store interface {
    SaveEntry(ctx context.Context, e Entry) error
    ListEntriesInRange(ctx context.Context, from, to time.Time) ([]Entry, error)
}
```

```go
// feature/diary/postgres.go — 唯一 database/sql を触る
type Postgres struct{ db *sql.DB }

func (p *Postgres) SaveEntry(ctx context.Context, e Entry) error {
    _, err := p.db.ExecContext(ctx, "INSERT INTO diary_entries ...")
    return err
}
```

```go
// feature/diary/task.go — Task interface 実装 shim、50 行以下
package diary

type Task struct {
    store  Store
    llm    llm.Client
    logger *slog.Logger
}

func NewTask(s Store, c llm.Client, l *slog.Logger) *Task {
    return &Task{store: s, llm: c, logger: l}
}

func (t *Task) Name() string { return "diary" }

func (t *Task) Run(ctx context.Context) error {
    return Write(ctx, t.store, t.llm, CurrentPeriod(time.Now()))
}
```

### 4.2 大規模 feature（例：`feature/memory/`）

ファイル数が増えるだけで構造は同じ：

```
feature/memory/
├── memory.go             # 公開型（Memo, Tag, MemoryType）+ Service 構造体
├── acquire.go            # 獲得ロジック
├── consolidate.go        # 統合ロジック
├── search.go             # FTS + Vec + Symbolic 検索
├── cluster.go            # union-find ヘルパー
├── judge.go              # LLM 判定ヘルパー
├── store.go              # interface Store
├── postgres.go           # Store 実装
└── memory_test.go
```

公開 API（`memory.Service`）が `port/memory.Memory` を満たすよう実装する。他 feature からは `port/memory` 経由のみ。

### 4.3 小規模 feature（例：`feature/forget/`）

1-2 ファイルで完結：

```
feature/forget/
├── forget.go             # ロジックと型をまとめる
├── task.go               # Task 実装
└── forget_test.go
```

`store.go` や `postgres.go` が不要なら省略。**構造テンプレは同じだが、使わないファイルは作らない**。

### 4.4 Tool のみの feature（例：`feature/video/`）

Task なし、Tool 複数：

```
feature/video/
├── video.go              # 公開型（Subtitle, Frame）
├── transcript.go         # 字幕取得ロジック（内部で port/transcript を使う）
├── look.go               # frame 解析ロジック（内部で port/llm を使う）
├── tool_watch.go         # tool.Tool 実装（字幕取得を公開）
├── tool_look.go          # tool.Tool 実装（frame 解析を公開）
└── video_test.go
```

---

## 5. 依存ルール

### 5.1 許可される import

| from → to | OK |
|---|---|
| `cmd/` → 任意 | ✓ DI 配線のため |
| `adapter/X/` → `port/X/` | ✓ 実装義務 |
| `adapter/X/` → `domain/` `lib/` | ✓ |
| `feature/X/` → `port/` | ✓ cross-cutting 利用 |
| `feature/X/` → `runtime/` | ✓ Session や event を使う場合 |
| `feature/X/` → `domain/` `lib/` | ✓ |
| `feature/X/` → `feature/Y/`（公開 API のみ） | ✓ 最小限に |
| `channel/X/` → `port/` `runtime/` `domain/` | ✓ |
| `api/admin/` → `feature/` `runtime/` `port/` | ✓ CRUD 管理のため |
| `api/control/` → `feature/` `runtime/` `port/` | ✓ ランタイム操作のため |
| `api/<X>/` → `api/<X>/gen/` | ✓ 自サーフェスの ogen コード |
| `runtime/` → `port/` `domain/` `lib/` | ✓ |
| `port/` → `domain/` `lib/` | ✓ interface 引数型 |

### 5.2 禁止される import

| from → to | ✕ | 理由 |
|---|---|---|
| `adapter/` → `feature/` `runtime/` `channel/` `api/` | ✕ | 層逆行 |
| `port/` → `runtime/` `feature/` `adapter/` | ✕ | 契約は実装を知らない |
| `runtime/` → `feature/` `channel/` `api/` `adapter/` | ✕ | runtime は plugin を知らない |
| `feature/X/` → `feature/Y/` の非公開（store.go / task.go 等） | ✕ | 別 feature の内部に立ち入り禁止 |
| `channel/X/` → `channel/Y/` | ✕ | sibling 禁止 |
| `adapter/X/` → `adapter/Y/` | ✕ | sibling 禁止 |
| `domain/` → 任意（`lib/` 除く） | ✕ | 純データ |
| `lib/` → 任意の内部 package | ✕ | プリミティブ |

### 5.3 feature 間の依存ガイドライン

優先順位：

1. **型だけ必要** → `domain/` に昇格
2. **interface 越しに使いたい** → `port/` に昇格
3. **相手の公開関数を直接呼びたい** → 相手の `<feature>.go` のみ import
4. **それでも足りない** → 設計を見直す

---

## 6. port と adapter のパターン

### 6.1 パターン A：feature 付き port

feature がロジックを持ち、他から呼ばれるときは port で公開：

```
feature/memory/memory.go     → port/memory.Memory を満たす実装
port/memory/memory.go        → interface Memory
```

他 feature：`import "internal/port/memory"` のみ。`feature/memory/` は直接触らない。

該当：`memory` `llm` `mcp`

### 6.2 パターン B：薄い port（feature 無し）

外部 SDK wrapper が本体で、suzuha 固有のロジックが無いもの：

```
port/embedder/embedder.go    → interface Embedder
adapter/embedder/gemini/     → Embedder 実装
```

`feature/embedder/` は作らない。利用側の feature（例：memory）が直接 `port/embedder.Embedder` を DI で受け取る。

該当：`embedder` `tts` `stt` `vad` `transcript`

### 6.3 パターン C：feature のみ（port 無し）

1 つの feature に閉じる機能：

```
feature/diary/               → task.go が scheduler.Task を満たす
```

他 feature から直接呼ばれないなら port は不要。runtime は `port/scheduler.Task` 経由で task.go を呼ぶだけ。

該当：`diary` `research` `wander` `voice` `vision` `location` `forget` `topics` `video` `action`

---

## 7. 廃止・統合対象

| 現在 | 目標 | 備考 |
|---|---|---|
| `external/` | 廃止 | 内容は `adapter/` へ |
| `internal/adapter/` | リネーム → `channel/` | プロトコル adapter として |
| `internal/feature/forget/` | `feature/forget/` 維持 | 既に小 feature 形に近い |
| `internal/feature/topics/` | `feature/topics/` 維持 | 同上 |
| `internal/feature/video/` | `feature/video/` 維持 | tool_watch / tool_look に役割分離 |
| `internal/feature/wander/` | `feature/wander/` に再整理 | task.go / tool.go / logic ファイル分離 |
| `internal/feature/research/` | `feature/research/` に再整理 | 同上 |
| `internal/feature/diary/` | `feature/diary/` に再整理 | 同上 |
| `internal/feature/action/` | `feature/action/` | |
| `internal/feature/vision/` | `feature/vision/` | |
| `internal/feature/location/` | `feature/location/` | |
| `internal/voice/` | `feature/voice/` | |
| `internal/memento/` + `internal/memory/` | `feature/memory/` に統合 | acquire / consolidate / search / store を 1 package に |
| `internal/memento/acquirer.Completer` `internal/memento/consolidator.Completer` | 重複解消 | memory 統合で 1 つに |
| `internal/llm/` | `feature/llm/` + `port/llm/` + `adapter/llm/<vendor>/` | |
| `internal/mcp/` | `feature/mcp/` + `port/mcp/` | |
| `internal/lib/` | `lib/` | 位置同じ |
| `internal/observe/` | `observe/`（framework 対象外） | |

---

## 8. 命名

- **ディレクトリ名** = 役割・ドメイン語彙
- **package 名** = ディレクトリ名と一致（Go 慣習）
- **feature 内のファイル** = §2 原則 2 の表に従う
- **adapter サブディレクトリ** = ベンダー・技術名（`openai` / `sqlite` / `voicevox`）
- **interface 名** = 動詞＋er（`Synthesizer`, `Transcriber`, `Embedder`）or ドメイン名（`Store`, `Client`, `Memory`）
- **禁止**：`utils/` `helpers/` `common/` `misc/` `base/` `shared/`

---

## 9. 配置判定フロー

```
新しい package / ファイルを作りたい
  │
  ├─ 外部サービス/DB の具象実装？
  │     → adapter/<kind>/<vendor>/
  │
  ├─ 入出力プロトコル（Discord, CLI, Web, Device）？
  │     → channel/<protocol>/
  │
  ├─ HTTP 管理 API？
  │     → データ CRUD なら api/admin/handler_<resource>.go
  │     → ランタイム操作なら api/control/handler_<area>.go
  │
  ├─ agent loop 自体の変更（pipeline/session 等）？
  │     → runtime/<name>/
  │
  ├─ 複数 feature が共有する interface？
  │     → port/<contract>/
  │
  ├─ 共有される Entity / 値型？
  │     → domain/<name>/
  │
  ├─ 個別機能のロジック？
  │     → feature/<name>/ （§4 のファイル命名に従う）
  │
  ├─ Task 実装？
  │     → feature/<name>/task.go
  │
  ├─ Tool 実装？
  │     → feature/<name>/tool_<verb>.go
  │
  └─ cross-cutting util（log, metrics）？
        → observe/ or lib/
```

---

## 10. 強制手段

- `depguard` lint rule で import 違反を静的検知
- CI で `go vet` + depguard を走らせる
- 違反はビルドエラー

---

## 11. スコープ外

- 現状からの **移行手順**（別ドキュメント）
- `runtime/pipeline/` 内部のファイル分割規則
- リント設定の具体：`.golangci.yml` / `.depguard.yml` で管理
