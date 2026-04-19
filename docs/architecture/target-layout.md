# アーキテクチャ設計書：パッケージ配置の目標形

## 0. このドキュメントは何か

現状の `agent/internal/` を、**`capability/` と `behavior/` の 2 群分離 ＋ 横断的な Ports & Adapters** に置き換える。本書はその最終形（ゴール）の定義。

- **capability**：agent が「持つ」能力（memory, llm, voice 等）。port を公開し、他から呼ばれる
- **behavior**：agent が「する」行動（diary, research, wander 等）。scheduler / LLM に駆動される

設計の根拠：

- 1 ドメインパッケージ = 1 directory → 「X を直す」は 1 ディレクトリで完結
- Ports & Adapters の契約／実装分離 → LLM・DB・TTS のような横断能力は port と adapter に切る
- サイズで構造を変えない → 小さいパッケージも大きいパッケージも同じファイル命名規則
- capability と behavior で性質が違う部分だけ扱いが違う（port の有無）

---

## 1. 採用する architecture

**Hexagonal Architecture (Ports & Adapters / Cockburn 2005)** × **Vertical Slice Architecture (Bogard 2017)** のハイブリッド。

- port / adapter は Hexagonal の語彙をそのまま採用
- パッケージの区切り（1 dir = 1 概念）は Vertical Slice の発想
- capability / behavior の 2 群分離は意味の違いを反映（独自の整理）
- DDD 由来の Entity / Value Object だけ借りる（Aggregate / Domain Event は使わない）
- Clean Architecture の 4 層・UseCase 層は **採用しない**
- Functional Core / Imperative Shell は **採用しない**（Go + LLM 中心の I/O には合わない）

---

## 2. 設計原則

### 原則 1：1 ドメインパッケージ = 1 directory

ドメイン単位のコードは **`capability/<name>/` か `behavior/<name>/`** に集める。サイズに関係なく同じ構造。

### 原則 1.5：capability / behavior で意味的に分ける

| 区分 | 定義 | port | 実例 |
|---|---|---|---|
| **capability**（能力） | agent が **持つ** 能力。他コード（runtime / behavior / channel / api）から呼ばれる | 必ず `port/<X>/` を公開 | memory, llm, voice, vision, location, mcp |
| **behavior**（行動） | agent が **する** こと。scheduler から走らされる or LLM から呼ばれる | 持たない（shim が port/scheduler.Task / port/tool.Tool を満たす） | diary, research, wander, forget, topics, action, video |

**判別基準**：

- 「他から呼ばれたいか？」 → YES なら capability
- 「自律的に走る or LLM が呼ぶか？」 → YES なら behavior
- 両方 → どちらの性質が強いかで決める。例：location は Overland 取り込み（behavior っぽい）＋ "どこにいる" を返す（capability）が、後者の方が本質 → capability

ブレを防ぐため、**capability 内は全部同じ構造、behavior 内は全部同じ構造**。2 群間で構造が違うのは意味の違いを反映しているだけ。

### 原則 2：ファイル単位で役割を分離

capability / behavior package 内で **1 ファイル = 1 責務**。癒着の原因は「ディレクトリに何でも入れる」ではなく「1 ファイルに複数責務を入れる」。

| 役割 | ファイル名 | 中身 |
|---|---|---|
| 公開 API | `<name>.go` | 公開型・公開関数（package 名と一致するファイル） |
| 純ロジック | `<verb>.go` | `search.go` / `write.go` / `consolidate.go` 等。I/O を直接触らない |
| 永続化契約 | `store.go` | Store interface（consumer-side） |
| 永続化実装 | `<storage>.go` | `postgres.go` / `sqlite.go` など |
| scheduler Task | `task.go` | `port/scheduler.Task` 実装 shim |
| LLM Tool | `tool_<verb>.go` | `port/tool.Tool` 実装 shim |
| HTTP handler | `handler.go` | admin 経由の操作（必要なら） |
| テスト | `<file>_test.go` | fake Store / fake LLM で完結 |

### 原則 3：cross-cutting は `port/` + `adapter/`

複数の capability / behavior で共有される能力（LLM / embedder / TTS / STT / VAD / 字幕取得）は capability 内に置かず、`port/` に契約、`adapter/` に実装を置く。

`scheduler.Task` / `tool.Tool` のような interface 自体も `port/` に集約する（shim の実装は各 behavior / capability 内の `task.go` / `tool_*.go`）。

### 原則 4：依存方向は一方向

```
cmd/                                     （DI、全部見える）
  ↓
adapter/                                 （port を実装）
  │
  │    port/                             （契約）
  │      ↑
  ├─── capability/<X>/                   （agent の能力、port 公開）
  │      │  ↓（port 経由のみ）
  │      └─ 他 capability/<Y>/           （sibling 間は必ず port 経由）
  │
  ├─── behavior/<X>/                     （agent の行動、shim ベース）
  │      │  ↓（port 経由のみ）
  │      └─ capability/<Y>/              （必ず port 経由）
  │
  ├── channel/                           （入出力プロトコル）
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

- **runtime は capability / behavior を知らない**（runtime は agent loop のみ、個別パッケージは port 越しに呼ぶ）
- **adapter は port だけを知る**（capability / behavior / runtime を import しない）
- **port は純契約**（実装側を一切知らない）

### 原則 5：sibling 間の直接 import 禁止

同種パッケージ間（capability 同士、behavior 同士、channel 同士、adapter 同士）の直接 import は禁止：

- capability/A → capability/B を使いたい → `port/B` 経由のみ
- behavior/A → behavior/B は禁止（共有ロジックは別 package に切り出す）
- behavior/A → capability/B を使いたい → `port/B` 経由のみ

共有が必要になったら：

- **型だけ共有** → `domain/<name>/` に昇格
- **interface を共有** → `port/<name>/` に昇格
- **ロジックを共有** → 片方に寄せるか、独立パッケージに切り出す

### 原則 6：capability / behavior 内で `database/sql` を直接使わない

`store.go` で interface を定義、`<storage>.go` がその実装。logic ファイルは Store interface だけを受け取る。これにより：

- fake Store でのユニットテストが書ける
- DB バックエンド差し替えが 1 ファイル内で済む

**例外**：schema migration は capability / behavior の責務外とする。`driver/store/<name>/migrations/` 等、driver 側または専用の migration tool に集約する。現行 `scheduler.Feature.Setup(ctx, db)` のような「feature 内での CREATE TABLE」は **廃止**。

### 原則 7：domain に出す型の判定

型をどこに置くかは **package 外から参照されるか** で決まる：

| 性質 | 置き場 | 例 |
|---|---|---|
| 複数 package から参照される Entity / Value Object | `domain/<name>/` | `domain/diary.Entry`（admin API と behavior/diary の両方で使う） |
| 1 package 内に閉じる中間構造 | その package 内 | `behavior/research/` 内部の検索パラメータ構造体 |
| enum 相当（型 + 定数） | 参照範囲次第。複数から使うなら domain/ | `domain/action.ActionStatus` |

判断の起点は「**新しい型を `package <X>` 内に書いた後、同じ型を別 package の import 文に書きたくなるか？**」。イエスなら `domain/` に出す。

### 原則 8：禁止 package 名

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
    ├── domain/                   # 全パッケージで共有される Entity / Value Object
    │   ├── memo/                 # Memo, MemoryType, Keywords
    │   ├── user/                 # User, PlatformLink, UserGuild, MentionableUser, GuildSummary, ChannelEntry, GuildChannel
    │   ├── message/              # Message, Role（llm から昇格）
    │   ├── channel/              # ChannelID, PlatformID, Source kind
    │   ├── diary/                # Entry, Period, EntryKind
    │   ├── action/               # ScheduledAction, ActionStatus
    │   └── location/             # LocationPoint, LocationArea, DeviceMapping, Place, UserLocation
    │   （research は外向け型無しのため domain 不要。behavior/research/ 内に閉じる）
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
    ├── capability/               # agent が持つ能力（他から呼ばれる、port あり）
    │   ├── memory/               # 記憶（旧 memento + memory を統合）
    │   ├── llm/                  # LLM プロバイダ管理
    │   ├── voice/                # VAD/STT/TTS 配線
    │   ├── vision/               # カメラ映像理解
    │   ├── location/             # GPS 取り込み + query
    │   └── mcp/                  # MCP クライアント管理
    │   （user は capability ではない：ロジックが無く純データストアのため
    │    domain/user/ + port/user/ + driver/store/user/ に分解する）
    │
    ├── behavior/                 # agent がする行動（scheduler / LLM に駆動される）
    │   ├── diary/                # 日記書き込み (Task)
    │   ├── research/             # 研究 (Task + Tool)
    │   ├── wander/               # 徘徊 (Task + Tool)
    │   ├── forget/               # 記憶忘却 (Task)
    │   ├── topics/               # 話題の暇度 (Task)
    │   ├── action/               # 予約アクション実行 (Task)
    │   └── video/                # 動画理解 (Tool 群のみ)
    │
    ├── channel/                  # 入出力プロトコル（独立扱い）
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
    │   │   ├── handler_<resource>.go  # リソース別 handler（capability / behavior を呼ぶ薄い shim）
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
    │   ├── tool/                 # Tool, ReadOnlyTool, ToolResult, Content（現行 internal/tool/ の interface 群を移動）
    │   ├── chat/                 # Sender, Interface, Replier, IDSender, Typer, VoiceSpeaker（現行 internal/chat/ を port 化）
    │   ├── user/                 # Store, AdminStore, BotRegistrar（現行 internal/user/ の interface 群）
    │   ├── llm/                  # Client（capability/llm の公開 API）
    │   │                         # 注：現行 *llm.Client は concrete struct。interface 抽出が要
    │   ├── memory/               # Memory（capability/memory の公開 API）
    │   ├── mcp/                  # Client（capability/mcp の公開 API）
    │   ├── embedder/             # Embedder（capability 無し、薄い port）
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

## 4. capability / behavior 内部の構造

capability と behavior は **同じファイル命名規則に従う**。違いは「port を公開するか」だけ。

### 4.1 behavior の標準形（例：`behavior/diary/`）

```
behavior/diary/
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
// behavior/diary/diary.go
package diary

type Entry struct {
    ID          string
    Kind        string
    Content     string
    PeriodStart time.Time
    PeriodEnd   time.Time
}

type Period struct{ Start, End time.Time }

// 公開 API。他 package はここだけ触れる
func CurrentPeriod(now time.Time) Period { ... }
```

```go
// behavior/diary/write.go — 純ロジック、I/O なし
func Write(ctx context.Context, s Store, llmCli llm.Client, p Period) error {
    existing, err := s.ListEntriesInRange(ctx, p.Start, p.End)
    // ...
    return s.SaveEntry(ctx, Entry{...})
}
```

```go
// behavior/diary/store.go — consumer-side interface
type Store interface {
    SaveEntry(ctx context.Context, e Entry) error
    ListEntriesInRange(ctx context.Context, from, to time.Time) ([]Entry, error)
}
```

```go
// behavior/diary/postgres.go — 唯一 database/sql を触る
type Postgres struct{ db *sql.DB }

func (p *Postgres) SaveEntry(ctx context.Context, e Entry) error {
    _, err := p.db.ExecContext(ctx, "INSERT INTO diary_entries ...")
    return err
}
```

```go
// behavior/diary/task.go — Task interface 実装 shim、50 行以下
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

### 4.2 capability の標準形（例：`capability/memory/`）

port を公開する点以外は behavior と同じ：

```
capability/memory/
├── memory.go             # 公開型（Memo, Tag, MemoryType）+ Service 構造体
│                         # Service は port/memory.Memory を実装
├── acquire.go            # 獲得ロジック
├── consolidate.go        # 統合ロジック
├── search.go             # FTS + Vec + Symbolic 検索
├── cluster.go            # union-find ヘルパー
├── judge.go              # LLM 判定ヘルパー
├── store.go              # interface Store（内部 consumer-side）
├── postgres.go           # Store 実装
└── memory_test.go
```

`port/memory.Memory` を `memory.Service` が実装する。他は `port/memory` 経由のみアクセス。

### 4.3 小規模 behavior（例：`behavior/forget/`）

1-2 ファイルで完結：

```
behavior/forget/
├── forget.go             # ロジックと型をまとめる
├── task.go               # Task 実装
└── forget_test.go
```

`store.go` や `postgres.go` が不要なら省略。**構造テンプレは同じだが、使わないファイルは作らない**。

### 4.4 Tool のみの behavior（例：`behavior/video/`）

Task なし、Tool 複数：

```
behavior/video/
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
| `capability/X/` → `port/` | ✓ cross-cutting 利用 |
| `capability/X/` → `domain/` `lib/` | ✓ |
| `capability/X/` → `runtime/` | △ 必要最小限（基本は port で間に合わせる） |
| `capability/X/` → 他 `capability/Y/` | **直接 import 禁止**、`port/Y/` 経由のみ |
| `capability/X/` → `behavior/` | ✕ capability は behavior を知らない |
| `behavior/X/` → `port/` | ✓ LLM / store 等を使う |
| `behavior/X/` → `runtime/` | ✓ Session や event を使う場合 |
| `behavior/X/` → `domain/` `lib/` | ✓ |
| `behavior/X/` → 他 `behavior/Y/` | ✕ sibling 禁止 |
| `behavior/X/` → `capability/Y/` | **直接 import 禁止**、`port/Y/` 経由のみ |
| `channel/X/` → `port/` `runtime/` `domain/` | ✓ |
| `channel/X/` → 他 `channel/Y/` | ✕ sibling 禁止 |
| `api/admin/` → `capability/` `behavior/` `runtime/` `port/` | ✓ CRUD 管理のため |
| `api/control/` → `capability/` `behavior/` `runtime/` `port/` | ✓ ランタイム操作のため |
| `api/<X>/` → `api/<X>/gen/` | ✓ 自サーフェスの ogen コード |
| `runtime/` → `port/` `domain/` `lib/` | ✓ |
| `port/` → `domain/` `lib/` | ✓ interface 引数型 |

### 5.2 禁止される import

| from → to | ✕ | 理由 |
|---|---|---|
| `adapter/` → `capability/` `behavior/` `runtime/` `channel/` `api/` | ✕ | 層逆行 |
| `port/` → `runtime/` `capability/` `behavior/` `adapter/` | ✕ | 契約は実装を知らない |
| `runtime/` → `capability/` `behavior/` `channel/` `api/` `adapter/` | ✕ | runtime は plugin を知らない |
| `capability/X/` ↔ 他 `capability/Y/` の直接 | ✕ | port 経由のみ |
| `behavior/X/` ↔ 他 `behavior/Y/` | ✕ | sibling 禁止 |
| `behavior/X/` → `capability/Y/` の直接 | ✕ | port 経由のみ |
| `channel/X/` ↔ `channel/Y/` | ✕ | sibling 禁止 |
| `adapter/X/` ↔ `adapter/Y/` | ✕ | sibling 禁止 |
| `domain/` → 任意（`lib/` 除く） | ✕ | 純データ |
| `lib/` → 任意の内部 package | ✕ | プリミティブ |

### 5.3 capability / behavior 間の依存ガイドライン

- **capability は他 capability を port 経由でしか使えない**：memory が llm を使うなら `port/llm` を import（`capability/llm` は直接触らない）
- **behavior は capability を port 経由でしか使えない**：diary が memory を使うなら `port/memory` を import
- **共有したい型があれば `domain/` に昇格**
- **sibling 間の直接依存は一切禁止**

これで「capability 同士の癒着」「behavior 同士の癒着」が構造的に防止される。

---

## 6. port と adapter のパターン

### 6.1 パターン A：capability + port（常にセット）

capability はロジックを持ち、他から呼ばれるので必ず port を公開：

```
capability/memory/memory.go  → port/memory.Memory を満たす実装
port/memory/memory.go        → interface Memory
```

他（behavior / channel / api）：`import "internal/port/memory"` のみ。`capability/memory/` は直接触らない。

該当：`memory` `llm` `voice` `vision` `location` `mcp`

#### 6.1.1 port 内の interface 分割指針

1 つの capability が複数用途で使われる場合、port/ の interface を分割する。現行 `internal/memory/` は 4 つの interface を持っているので指針として：

| 用途 | port の分け方 | 現行 memory の例 |
|---|---|---|
| agent pipeline が使う主機能 | `port/memory.Memory` | `Store`（の一部） |
| admin CRUD | `port/memory.Admin` | `AdminStore` |
| media 添付管理 | `port/memory.Media` | `MediaStore` |
| DB 接続 / lifecycle | driver 側に閉じる | `Backend`（DB を返すだけ、port 化不要） |

**判断基準**：consumer が「何をしたいか」で interface を切る。1 つの巨大 interface を避け、3〜5 メソッド程度の狭い interface を複数用意。

該当する分割候補：
- **memory**：Memory / Admin / Media（Backend は port 化せず、driver/ 内部で閉じる）
- **llm**：Client（complete 系）／ Admin（presets CRUD 系）の分離候補
- **mcp**：Client（tool 呼び出し）／ Manager（server 管理）の分離候補

### 6.2 パターン B：薄い port（capability 無し）

外部 SDK wrapper が本体で、suzuha 固有のロジックが無いもの：

```
port/embedder/embedder.go    → interface Embedder
adapter/embedder/gemini/     → Embedder 実装
```

`capability/embedder/` は作らない。利用側（例：memory capability）が直接 `port/embedder.Embedder` を DI で受け取る。

該当：`embedder` `tts` `stt` `vad` `transcript`

### 6.3 パターン C：behavior（port 無し）

port を公開しない。shim を通じて scheduler / tool registry に登録されるのみ：

```
behavior/diary/               → task.go が scheduler.Task を満たす
```

他から直接呼ばれないなら port は不要。runtime は `port/scheduler.Task` 経由で task.go を呼ぶだけ。

該当：`diary` `research` `wander` `forget` `topics` `video` `action`

### 6.4 パターン D：channel 固有の Tool

プロトコル固有の操作（Discord の voice_join / voice_leave など）は channel 内に置く：

```
channel/discord/
├── discord.go
├── source.go
├── session.go
├── tool_voice_join.go    # Discord 特有の Tool（tool.Tool 実装）
└── tool_voice_leave.go
```

- channel 内の `tool_*.go` も `port/tool.Tool` を実装する
- DI で tool registry に register
- 他の channel / behavior からは直接触らない（tool registry 経由）

behavior と channel の違い：
- **behavior/**：プロトコル非依存の自律行動（diary / research 等）
- **channel/**：特定プロトコル固有の入出力 + そのプロトコル固有 Tool

---

## 7. 廃止・統合対象

| 現在 | 目標 | 備考 |
|---|---|---|
| `external/` | 廃止 | 内容は `adapter/` へ |
| `internal/adapter/` | リネーム → `channel/` | プロトコル adapter として |
| `internal/admin/` | `internal/api/admin/` | HTTP サーフェスとして |
| `agent/{cli,device,discord,web}_session.go` | **移動**：各 `channel/<name>/session.go` へ | Session は入出力プロトコル固有なので channel/ に属する |
| web 入力経路（現状 hub 経由で散在） | `channel/web/` に集約 | 他 adapter と揃える（source.go / session.go を揃える） |
| `internal/chat/` | **そのまま `port/chat/` に移動** | 既に interface のみの package。Go 慣習に沿った consumer-side interface として完成済み |
| `internal/tool/tool.go` (interfaces) | `port/tool/` へ | Tool, ReadOnlyTool, ToolResult, Content を port 化 |
| `internal/tool/registry.go` (Registry) | `runtime/toolregistry/` へ | 実装は runtime に残す |
| `internal/user/user.go` の Entity 群 | `domain/user/` に移動 | User / PlatformLink / UserGuild 等 7 型 |
| `internal/user/user.go` の interface 群 | `port/user/` へ | Store / AdminStore / BotRegistrar |
| `internal/user/store.go` | `driver/store/user/` へ | DBStore 実装（SQL） |
| `scheduler.Feature` interface | **廃止** | behavior は `task.go` / `tool_*.go` で個別に port/scheduler.Task / port/tool.Tool を満たす。DI で登録 |
| `Feature.Setup(ctx, *sql.DB)` | **廃止** | schema migration は `driver/store/<name>/migrations/` or 専用 migration tool に分離 |
| `api/admin/store.go` の shadow 型 | **廃止** | `Action` / `ActionListOpts` / `ActionUpdateFields` / `DiaryEntry` / `Location` / `UserLocation` / `DeviceMapping` / `Place` の 8 型全て `domain/<name>/` に一本化 |
| `api/admin/store.go` の shadow interface | **廃止** | `ActionStore` / `DiaryStore` / `LocationStore` の 3 interface は domain 統合により不要に（admin が直接 capability の port を使う） |
| `di/admin_adapter.go` の型変換関数 | **廃止** | `diaryStoreAdapter` / `diaryReaderAdapter` 等の変換は domain 統合で不要 |
| `agent/memory.Store` 直接 import（10 箇所） | **`port/memory.Memory` 経由に置換** | runtime / behavior / tool が `port/memory` のみ知る形に移行（最大の refactor 範囲） |
| `tool/builtin/memo.go` の `memory.AdminStore` 依存 | memo 専用の狭い interface に分離 | `port/memory.Admin` 全体ではなく memo が必要なメソッドだけの消費側 interface（consumer-side） |
| scheduler の広範な依存（channel / llm / memory / tool / user） | Feature 廃止で連鎖的に解消 | scheduler は port/scheduler.Task のみ知る |
| conversation の `llm.Message` 依存 | `domain/message/` に昇格で解消 | Message を domain に出せば conversation → domain/message のみ |
| `internal/memento/` + `internal/memory/` | **`capability/memory/` に統合** | acquire / consolidate / search / store を 1 package に。port/memory を新設 |
| `internal/memento/acquirer.Completer` `internal/memento/consolidator.Completer` | 重複解消 | memory 統合で 1 つに |
| `internal/llm/` | `capability/llm/` + `port/llm/` + `adapter/llm/<vendor>/` | |
| `internal/mcp/` | `capability/mcp/` + `port/mcp/` | |
| `internal/voice/` | `capability/voice/` + `port/{stt,tts,vad}/` + `adapter/{stt,tts,vad}/*/` | VAD/STT/TTS は個別 port 分解 |
| `internal/feature/vision/` | **`capability/vision/`** + `port/vision/` | camera pipeline は capability |
| `internal/feature/location/` | **`capability/location/`** + `port/location/` | GPS store + query は capability |
| `internal/feature/diary/` | **`behavior/diary/`** + **`domain/diary/`** に分割 | Entry/Period を domain/diary に出す |
| `internal/feature/research/` | **`behavior/research/`** に再整理 | task.go / tool_*.go / search.go / fetch.go 等 |
| `internal/feature/wander/` | **`behavior/wander/`** に再整理 | task.go / tool.go / wander.go |
| `internal/feature/forget/` | **`behavior/forget/`** 維持 | 既に小 behavior 形 |
| `internal/feature/topics/` | **`behavior/topics/`** 維持 | 同上 |
| `internal/feature/video/` | **`behavior/video/`** 維持 | tool_watch / tool_look 形 |
| `internal/feature/action/` | **`behavior/action/`** + **`domain/action/`** | ScheduledAction を domain に出す |
| `internal/feature/location/` の型 | **`domain/location/`** に分離 | LocationPoint, LocationArea |
| `internal/feature/diary/` の型 | **`domain/diary/`** に分離 | Entry, Period, EntryKind |
| `internal/lib/` | `lib/` | 位置同じ |
| `internal/observe/` | `observe/`（framework 対象外） | 層ルール対象外 |

---

## 8. 命名

- **ディレクトリ名** = 役割・ドメイン語彙
- **package 名** = ディレクトリ名と一致（Go 慣習）
- **capability / behavior 内のファイル** = §2 原則 2 の表に従う
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
  ├─ 共有される Entity / 値型？
  │     → domain/<name>/
  │
  ├─ 複数が共有する interface？
  │     → port/<contract>/
  │
  ├─ agent が「持つ」能力（他から呼ばれる、port あり）？
  │     → capability/<name>/ + port/<name>/
  │
  ├─ agent が「する」行動（scheduler / LLM に駆動される）？
  │     → behavior/<name>/
  │
  ├─ Task 実装？
  │     → behavior/<name>/task.go
  │
  ├─ Tool 実装？
  │     → behavior/<name>/tool_<verb>.go（or capability/<name>/tool_<verb>.go）
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
