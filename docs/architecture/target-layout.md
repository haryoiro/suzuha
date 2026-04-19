# アーキテクチャ設計書：パッケージ配置の目標形

## 0. このドキュメントは何か

現状の `agent/internal/` を、**`capability/` と `behavior/` の 2 群分離 ＋ 横断的な Ports & Adapters** に置き換える。本書はその最終形（ゴール）の定義。

- **capability**：agent が「持つ」能力（memory, llm, voice 等）。port を公開し、他から呼ばれる
- **behavior**：agent の自律行動（research, video, action）。LLM を使って意思を持って行う

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
| **capability**（能力） | agent が **持つ** 能力。他コード（runtime / behavior / channel / api）から呼ばれる。**maintenance task も含みうる**（記憶の acquire/consolidate/forget/summarize 等） | 必ず `port/<X>/` を公開 | memory, llm, voice, vision, conversation, mcp |
| **behavior**（行動） | agent の **自律行動**（LLM を使って意思を持って行う）| 持たない（shim が port/scheduler.Task / port/tool.Tool を満たす） | research, action, video |

**判別基準**：

- 「他から呼ばれたいか？」 → YES なら capability
- 「自律的に走る or LLM が呼ぶか？」 → YES なら behavior
- 両方 → どちらの性質が強いかで決める
- **迷ったら capability**（port を切っておけば後で behavior 化は容易。逆は困難）

#### エッジケース一覧

| ケース | 判定 | 理由 |
|---|---|---|
| `action` | behavior | 予約アクションの自律実行が主。admin からの参照は `port/scheduler` 越しで済む（直接 capability 化不要） |
| `topics` | capability/conversation 内 task | 暇度計算は maintenance。`task_boredom.go` として capability/conversation に統合 |
| `diary` | capability/memory 内 task | 会話要約は maintenance。`task_summarize.go` として capability/memory に統合 |
| `forget` | capability/memory 内 task | 記憶削除は maintenance。`task_forget.go` として capability/memory に統合 |
| `conversation` | capability | 会話履歴 + channel settings、pipeline と admin 両方が読み書き |
| `vision` | capability | camera pipeline は hot path、複数 channel（device/control API）から使う |
| `mcp` | capability | MCP server は差し替え / 追加ありうる、tool 提供の main |
| `video` | behavior | Tool 2 個のみ、自律 task なし、外部に公開すべき能力なし |

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
| LLM Tool | `tool_<verb>.go` | `port/tool.Tool` 実装 shim。**1 tool = 1 ファイル** を厳守 |
| HTTP handler | `handler.go` | admin 経由の操作（必要なら） |
| テスト | `<file>_test.go` | fake Store / fake LLM で完結 |

### 原則 3：cross-cutting は `port/` + `adapter/`

複数の capability / behavior で共有される能力（LLM / embedder / TTS / STT / VAD / 字幕取得）は capability 内に置かず、`port/` に契約、`adapter/` に実装を置く。

`scheduler.Task` / `tool.Tool` のような interface 自体も `port/` に集約する（shim の実装は各 behavior / capability 内の `task.go` / `tool_*.go`）。

### 原則 4：依存方向は一方向

矢印は「上段が下段を import できる」。同段 sibling 間は禁止。下段から上段への import も禁止。

```
┌─────────────────────────────────────────────────────────────────┐
│ cmd/                                          （composition root）│
│ すべての層を import して DI 配線する                             │
└──────────────────────┬──────────────────────────────────────────┘
                       │
      ┌────────────────┼─────────────────┬──────────┐
      ▼                ▼                 ▼          ▼
┌───────────┐   ┌────────────┐   ┌────────────┐  ┌──────────┐
│ api/      │   │ channel/   │   │ adapter/   │  │ behavior/│
│ (admin,   │   │ (discord,  │   │ (llm, tts, │  │ (research,│
│  control) │   │  device,   │   │  stt, ...) │  │ research)│
└─────┬─────┘   │  web, cli) │   └──────┬─────┘  └─────┬────┘
      │         └──────┬─────┘          │              │
      │                │                │              │
      ▼                ▼                ▼              ▼
      ┌─────────────────────┐    ┌─────────────────────────┐
      │ capability/         │───▶│ port/ （契約）           │
      │ (memory, llm, ...)  │◀───│ adapter が実装する契約  │
      └──────────┬──────────┘    └───────────┬─────────────┘
                 │  （behavior/ channel/ api/ も port を import）
                 └────────────┬──────────────┘
                              ▼
                 ┌─────────────────────┐
                 │ runtime/            │
                 │ (agent loop)        │
                 └──────────┬──────────┘
                            ▼
                 ┌─────────────────────┐
                 │ domain/             │
                 │ (Entity / VO)       │
                 └──────────┬──────────┘
                            ▼
                 ┌─────────────────────┐
                 │ lib/                │
                 └──────────┬──────────┘
                            ▼
                          stdlib
```

- **runtime は capability / behavior を知らない**（runtime は agent loop のみ、個別パッケージは port 越しに呼ぶ）
- **adapter は port だけを知る**（capability / behavior / runtime を import しない）
- **port は純契約**（実装側を一切知らない）
- **同段 sibling 間の直接 import は禁止**（capability 同士、behavior 同士、channel 同士、adapter 同士）

### 原則 5：sibling 間の直接 import 禁止

同種パッケージ間（capability 同士、behavior 同士、channel 同士、adapter 同士）の直接 import は禁止：

- capability/A → capability/B を使いたい → `port/B` 経由のみ
- behavior/A → behavior/B は禁止
- behavior/A → capability/B を使いたい → `port/B` 経由のみ

共有が必要になったら、**共有物の性質に応じて 3 層のどれかに落とす**：

| 共有したいもの | 落とし先 | 例 |
|---|---|---|
| **型・値型** | `domain/<name>/` | `domain/action.ScheduledAction`（admin と behavior/action で共有） |
| **interface / 契約** | `port/<name>/` | `port/memory.Memory`（多くの consumer から呼ばれる） |
| **純ロジック（関数群）** | 内容に応じて 3 層 | 下記 |

純ロジックの落とし先判定：

1. **汎用プリミティブ**（時刻計算、文字列正規化、HTML パース等） → `lib/<name>/`
2. **agent loop 本体と絡む**（会話履歴取得、pipeline ヘルパ等） → `runtime/<name>/` に追加
3. **特定ドメイン固有だが複数 behavior で使う**（要約プロンプト生成など） → 新規 `internal/<name>/` に中立 subsystem を切る

多くの "共有ロジック" は 1 or 2 で解決する。3 になるのは稀で、**2 つ以上の behavior が具体的に import したくなってから** subsystem を切る（予防的に作らない）。

### 原則 6：capability / behavior 内で `database/sql` を直接使わない

`store.go` で interface を定義、`<storage>.go` がその実装。logic ファイルは Store interface だけを受け取る。これにより：

- fake Store でのユニットテストが書ける
- DB バックエンド差し替えが 1 ファイル内で済む

**例外**：schema migration は capability / behavior の責務外とする。`adapter/store/<name>/migrations/` 等、driver 側または専用の migration tool に集約する。現行 `scheduler.Feature.Setup(ctx, db)` のような「feature 内での CREATE TABLE」は **廃止**。

### 原則 7：domain に出す型の判定

原則 5 は「**既に存在するものを共有したくなったとき**」の切り出し先、原則 7 は「**新規に型を書くとき**」の配置判定。

型をどこに置くかは **package 外から参照されるか** で決まる：

| 性質 | 置き場 | 例 |
|---|---|---|
| 複数 package から参照される Entity / Value Object | `domain/<name>/` | `domain/action.ScheduledAction`（admin API と behavior/action の両方で使う） |
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
│   ├── suzuha-bench/
│   └── suzuha-synth/
│
└── internal/
    ├── lib/                      # stdlib 補完
    │   ├── jtime/
    │   ├── crypto/
    │   └── ...
    │
    ├── observe/                  # 【framework 対象外】cross-cutting util
    │   │                         # metrics / tracing / log ring buffer（副作用あり）
    │   │                         # 層ルール対象外、誰でも import 可
    │   │                         # 位置：internal/observe/（internal 配下、ただし層規則を適用しない）
    │   └── ...
    │
    ├── domain/                   # 全パッケージで共有される Entity / Value Object
    │   ├── memo/                 # Memo, MemoryType, Keywords
    │   ├── user/                 # User, PlatformLink, UserGuild, MentionableUser, GuildSummary, ChannelEntry, GuildChannel
    │   ├── message/              # Message, Role
    │   ├── channel/              # ChannelID, PlatformID, Source kind, Mode, Settings（旧 internal/channel/ の型を吸収）
    │   └── action/               # ScheduledAction, ActionStatus
    │   （research は外向け型無しのため domain 不要。behavior/research/ 内に閉じる）
    │   （diary は capability/memory/ に統合、domain 不要）
    │   （location は廃止）
    │
    ├── runtime/                  # agent loop そのもの
    │   ├── agent/                # オーケストレータ・ライフサイクル
    │   ├── pipeline/             # Perceive / Think / Act / Reflect
    │   ├── session/              # Session interface と共通実装（per-source の抽象）
    │   │                         # プロトコル固有の Session 実装は channel/<name>/session.go
    │   ├── gateway/              # Source 登録 hub (errgroup)
    │   ├── scheduler/            # cron runner + Task registry
    │   ├── toolregistry/         # Tool の登録と解決
    │   ├── conversation/         # 会話履歴バッファ
    │   └── event/                # イベントバス
    │
    ├── capability/               # agent が持つ能力（他から呼ばれる、port あり）
    │   ├── memory/               # 記憶（旧 memento + memory を統合）
    │   │                         # tasks: acquire / consolidate / forget / summarize（旧 diary）
    │   ├── llm/                  # LLM プロバイダ管理
    │   ├── voice/                # VAD/STT/TTS 配線
    │   ├── vision/               # カメラ映像理解
    │   ├── conversation/         # 会話履歴 + channel activity/settings
    │   │                         # tasks: boredom（旧 topics）
    │   └── mcp/                  # MCP クライアント管理
    │   （location は廃止。diary/forget → memory、topics → conversation に統合）
    │   （user は capability ではない：ロジックが無く純データストアのため
    │    domain/user/ + port/user/ + adapter/store/user/ に分解する）
    │
    ├── behavior/                 # agent の自律行動（LLM を使う能動的な行為）
    │   ├── research/             # 研究 (Task + Tool)
    │   ├── action/               # 予約アクション実行 (Task + Tool)
    │   └── video/                # 動画理解 (Tool 群のみ)
    │   （diary/forget は capability/memory/ の task に統合）
    │   （topics は capability/conversation/ の task に統合）
    │   （wander は廃止）
    │
    ├── channel/                  # 入出力プロトコル（独立扱い）
    │   ├── discord/              # source.go / session.go / sender.go / tool_*.go
    │   ├── device/               # ESP32 WebSocket
    │   ├── web/                  # Web widget
    │   └── cli/
    │   （各 channel は runtime/session.Session を実装する。共通部は runtime/session/）
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
    │   ├── scheduler/            # Task interface のみ（Scheduler 実装は runtime/scheduler/）
    │   ├── tool/                 # Tool, ReadOnlyTool, ToolResult, Content（現行 internal/tool/ の interface 群を移動）
    │   ├── chat/                 # Sender, Interface, Replier, IDSender, Typer, VoiceSpeaker（現行 internal/chat/ を port 化）
    │   ├── user/                 # Store, AdminStore, BotRegistrar（現行 internal/user/ の interface 群）
    │   ├── conversation/         # ChannelSettingsStore, ChannelActivityStore（capability/conversation の公開 API）
    │   ├── llm/                  # Client（capability/llm の公開 API）
    │   │                         # 注：現行 *llm.Client は concrete struct。interface 抽出が要
    │   ├── memory/               # Memory（capability/memory の公開 API）
    │   ├── mcp/                  # Client（capability/mcp の公開 API）
    │   ├── embedder/             # Embedder（capability 無し、薄い port）
    │   ├── tts/                  # Synthesizer
    │   ├── stt/                  # Transcriber
    │   ├── vad/                  # VoiceActivityDetector
    │   ├── vision/               # FrameProcessor（将来切る場合のみ、保留扱い）
    │   └── transcript/           # VideoTranscriptFetcher
    │   （location は廃止。search は 1-consumer のため port なし、behavior/research/ 内部）
    │   （detect/yolo も 1-consumer のため port なし、capability/vision/ 内部）
    │
    ├── adapter/                  # port 実装（外部 SDK / 永続化 / webhook）
    │   │
    │   │ ── 外部 SDK ──
    │   ├── llm/
    │   │   ├── openai/
    │   │   ├── zhipu/
    │   │   └── gemini/
    │   ├── embedder/{gemini,openai}/
    │   ├── tts/{voicevox,sbv2}/
    │   ├── stt/{deepgram,whisper}/
    │   ├── vad/
    │   ├── transcript/
    │   └── twitter/              # 複数 consumer（agent TweetFetcher + builtin/fetch）ゆえ adapter 化
    │   （detect は capability/vision/ 内部、search は behavior/research/ 内部に吸収）
    │   │
    │   │ ── 永続化（SQL 等）──
    │   └── store/
    │       ├── memory/           # memory.Store の SQL 実装 + migrations/
    │       │                     # diary_entries schema もここに統合（旧 feature/diary）
    │       ├── conversation/     # ChannelSettings / ChannelActivity の SQL 実装
    │       ├── research/
    │       ├── user/
    │       └── action/
    │       （location 関連は廃止：location capability / port / webhook すべて削除）
    │
    └── di/                       # composition root
```

---

## 4. capability / behavior 内部の構造

capability と behavior は **同じファイル命名規則に従う**。違いは「port を公開するか」だけ。

### 4.1 behavior の標準形（例：`behavior/action/`）

```
behavior/action/
├── action.go             # 公開型（Action, ActionStatus, ListOpts）
├── schedule.go           # 純ロジック：Action を組み立てて検証
├── query.go              # 純ロジック：status やチャンネルで抽出
├── store.go              # interface Store { Create, List, Update, Delete }
├── postgres.go           # SQL 実装（database/sql はここだけ）
├── task.go               # scheduler.Task 実装 shim（cron で期限到来を拾う）
├── tool_schedule.go      # LLM tool: 予約を入れる
└── action_test.go        # fake Store + fake LLM でテスト
```

コード例：

```go
// behavior/action/action.go
package action

type ActionStatus string

const (
    StatusPending  ActionStatus = "pending"
    StatusRunning  ActionStatus = "running"
    StatusDone     ActionStatus = "done"
    StatusFailed   ActionStatus = "failed"
)

type Action struct {
    ID          string
    ChannelID   string
    Content     string
    ScheduledAt time.Time
    Status      ActionStatus
}

// 公開 API。他 package はここだけ触れる
func Due(a Action, now time.Time) bool { return !a.ScheduledAt.After(now) }
```

```go
// behavior/action/schedule.go — 純ロジック、I/O なし
func Schedule(ctx context.Context, s Store, a Action) error {
    if a.Content == "" {
        return errors.New("content is empty")
    }
    // ...
    return s.Create(ctx, &a)
}
```

```go
// behavior/action/store.go — consumer-side interface
type Store interface {
    Create(ctx context.Context, a *Action) error
    List(ctx context.Context, opts ListOpts) ([]Action, error)
    Update(ctx context.Context, id string, fields UpdateFields) error
    Delete(ctx context.Context, id string) error
}
```

```go
// behavior/action/postgres.go — 唯一 database/sql を触る
type Postgres struct{ db *sql.DB }

func (p *Postgres) Create(ctx context.Context, a *Action) error {
    _, err := p.db.ExecContext(ctx, "INSERT INTO actions ...")
    return err
}
```

```go
// behavior/action/task.go — Task interface 実装 shim、50 行以下
package action

type Task struct {
    store  Store
    notify Notifier
    logger *slog.Logger
}

func NewTask(s Store, n Notifier, l *slog.Logger) *Task {
    return &Task{store: s, notify: n, logger: l}
}

func (t *Task) Name() string { return "action" }

func (t *Task) Run(ctx context.Context) error {
    pending, err := t.store.List(ctx, ListOpts{Status: string(StatusPending)})
    if err != nil { return err }
    now := time.Now()
    for _, a := range pending {
        if Due(a, now) {
            // 期限到来: 通知して status を更新
        }
    }
    return nil
}
```

### 4.2 capability の標準形（例：`capability/memory/`）

port を公開 + maintenance task を内包する点以外は behavior と同じ：

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
│
│ ── maintenance tasks（旧 diary / forget を統合）──
├── task_acquire.go       # 定期的に会話ログから Memo を抽出（scheduler.Task 実装）
├── task_consolidate.go   # 類似 Memo を統合
├── task_forget.go        # 古い Memo を忘却（旧 behavior/forget）
├── task_summarize.go     # 会話要約を保存（旧 behavior/diary/hourly + daily）
│
└── memory_test.go
```

`port/memory.Memory` を `memory.Service` が実装する。他は `port/memory` 経由のみアクセス。

**task は capability 内の shim**。`port/scheduler.Task` を満たし、DI で scheduler に直接登録される。behavior と区別する理由：これらは agent の自律行動ではなく、memory capability のライフサイクル維持（maintenance）だから。

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
| `channel/X/` → `capability/Y/` | ✕ **直接 import 禁止**、`port/Y/` 経由のみ |
| `channel/X/` → `behavior/` | ✕ channel は behavior を知らない |
| `channel/X/` → 他 `channel/Y/` | ✕ sibling 禁止 |
| `api/admin/` → `capability/` `behavior/` `runtime/` `port/` | ✓ CRUD 管理のため |
| `api/control/` → `capability/` `behavior/` `runtime/` `port/` | ✓ ランタイム操作のため |
| `api/<X>/` → `channel/` | △ channel の状態取得が必要な場合のみ（稀、runtime の status 経由を優先） |
| `api/<X>/` → `api/<X>/gen/` | ✓ 自サーフェスの ogen コード |
| `api/<X>/` → `api/<Y>/` | ✕ admin と control は独立 |
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
- **behavior は capability を port 経由でしか使えない**：action が memory を使うなら `port/memory` を import
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

該当：`memory` `llm` `voice` `vision` `conversation` `mcp`


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
behavior/action/              → task.go が scheduler.Task を満たす
```

他から直接呼ばれないなら port は不要。runtime は `port/scheduler.Task` 経由で task.go を呼ぶだけ。

該当：`research` `action` `video`

**厳密な定義**：behavior は "agent が LLM を使って意思を持って行う自律行動"。単なる maintenance task（記憶削除・会話要約・暇度計算等）は capability/ の task に統合する。

### 6.4 パターン D：channel 固有 / capability 固有の Tool

Tool 実装（`port/tool.Tool` を満たすもの）は原則 `behavior/<name>/tool_*.go` に置くが、**その Tool が特定の channel / capability の機能を直接公開しているだけなら** その package 内に置いてもよい：

```
channel/discord/
├── discord.go
├── source.go
├── session.go
├── tool_voice_join.go    # Discord 特有の Tool（tool.Tool 実装）
└── tool_voice_leave.go

capability/vision/
├── vision.go
├── pipeline.go
├── tool_look.go          # vision の frame 解析を LLM に公開
└── tool_tracker.go
```

**配置判定**：

| Tool の性質 | 置き場 |
|---|---|
| プロトコル固有の操作（discord voice、device LED 制御 等） | `channel/<name>/tool_*.go` |
| capability の機能を薄くラップして LLM に公開（vision.Look / mcp.Call 等） | `capability/<name>/tool_*.go` |
| capability を組み合わせた独立行動（research の web_search 等） | `behavior/<name>/tool_*.go` |
| 汎用 Tool（memo, skip_response 等） | `behavior/<name>/tool_*.go` or 独立 `behavior/builtin/tool_*.go` |

**共通ルール**：

- **1 tool = 1 ファイル**。`tool_web_search.go` は web_search 1 個のみ、`tool_web_fetch.go` は別ファイル。複数 tool を 1 ファイルに詰めない
- どの場所の `tool_*.go` も `port/tool.Tool` を実装する
- DI で tool registry に register
- sibling（他の channel / behavior / capability）から直接 import してはならない。tool registry 経由で呼ぶ

behavior と channel と capability の区別：
- **behavior/**：プロトコル非依存の自律行動（action / research / video 等）
- **channel/**：特定プロトコル固有の入出力 + そのプロトコル固有 Tool
- **capability/**：共有能力本体 + その能力を直接公開する Tool

### 6.5 port の置き場と分割原則

**集約ルール**：

- **複数 consumer が共有する interface は `port/<name>/`**（cross-package 契約）
- **1 package 内でのみ使う interface はその package 内**（consumer-side、Go 慣習に沿う）
  - 例：`capability/memory/store.go` の `Store` interface（memory が自分の永続化に必要なものを定義、外には出さない）
- **capability の tool shim は factory で `port/tool.Tool` を返す**（concrete type を露出させない）
  - 例：`capability/vision/tool_look.go` の `NewLookTool(...) port/tool.Tool`
  - DI で registry に登録するときも interface 型で扱う

**分割指針**：1 capability が複数用途で使われるなら port を分割：

| 用途 | port の分け方 | 現行 memory の例 |
|---|---|---|
| agent pipeline が使う主機能 | `port/memory.Memory` | `Store`（の一部） |
| admin CRUD | `port/memory.Management` | `AdminStore` |
| media 添付管理 | `port/memory.Media` | `MediaStore` |
| DB 接続 / lifecycle | adapter 側に閉じる | `Backend`（DB を返すだけ、port 化不要） |

**判断基準**：consumer が「何をしたいか」で interface を切る。1 つの巨大 interface を避け、3〜5 メソッド程度の狭い interface を複数用意。

該当する分割候補：
- **memory**：Memory / Management / Media（Backend は port 化せず、adapter/ 内部で閉じる）
- **llm**：Client（complete 系）／ Management（presets CRUD 系）の分離候補
- **mcp**：Client（tool 呼び出し）／ Manager（server 管理）の分離候補

### 6.6 テスタビリティ原則

capability / behavior は以下を満たす設計にする：

- **fake Store**（in-memory 実装）で単体テストが通る
- **fake LLM / fake Embedder** で外部依存なしにテストが書ける
- **runtime に依存しない**ロジックは pure 関数として書き、Session 等は引数で受け取る

テスト容易性は port パターン採用の最大の見返り。テストが書きにくいコードは設計の赤信号。

---

## 7. 廃止・統合対象

### 7.0 事前削除（migration 開始前）

以下は本設計の対象外。migration 開始前（Phase 0）にコードベースから削除する：

| 現行 | 削除理由 |
|---|---|
| `internal/feature/wander/` (524 行) | 自律徘徊、実用上筋悪のため廃止 |
| `internal/feature/location/` (1200+ 行) | GPS 取り込み、機能継続しないため廃止 |
| `api/admin/handler_location.go`、`spec/admin/routes/location.tsp` | location 依存、連動廃止 |
| `api/control/` の location 依存部 | 同上 |
| DI provider.go の wander / location 登録 | 連動削除 |

### 7.1 階層レベルの変更

| 現在 | 目標 | 備考 |
|---|---|---|
| `external/` | 廃止 | 7 package の移行先は個別判断：<br>・transcript / embedder / tts / stt / twitter → `adapter/` 配下（複数 consumer あり or 汎用）<br>・detect（YOLO）→ `capability/vision/` 内部（1-consumer）<br>・search → `behavior/research/` 内部（1-consumer） |
| `internal/adapter/{cli,device,discord}/` | **移動** → `internal/channel/{cli,device,discord}/` | プロトコル adapter は channel/ に |
| 新設 `internal/adapter/` | cross-cutting 実装用（llm/tts/store 等） | 旧 adapter との用途違いに注意（新設） |
| `internal/admin/` | `internal/api/admin/` | HTTP サーフェスとして（既に現状で進行中） |
| `internal/channel/` （activity.go / provider.go / settings.go） | **分解**：型は `domain/channel/` 吸収、Store は `capability/conversation/` | 旧 channel/ は「チャンネル状態」の capability、新 channel/ は「プロトコル adapter」で用途が別 |
| `internal/lib/` | `lib/` | 位置同じ、L0 として明示 |
| `internal/observe/` | `internal/observe/` のまま | 位置は internal 配下。層ルール対象外（framework exempt）で誰でも import 可 |

### 7.2 capability 昇格

| 現在 | 目標 | 備考 |
|---|---|---|
| `internal/memento/` + `internal/memory/` | **`capability/memory/`** に統合 + `port/memory/` 新設 | acquire / consolidate / search / store を 1 package に。最大の refactor |
| `internal/llm/` | **`capability/llm/`** + `port/llm/` + `adapter/llm/<vendor>/` | concrete *llm.Client → interface 抽出 |
| `internal/mcp/` | **`capability/mcp/`** + `port/mcp/` | Client / Manager 分離候補 |
| `internal/voice/` | **`capability/voice/`** + `port/{stt,tts,vad}/` + `adapter/{stt,tts,vad}/*/` | VAD/STT/TTS 個別 port 分解 |
| `internal/feature/vision/` | **`capability/vision/`** + `port/vision/` | camera pipeline、control API と device から使用 |

### 7.3 behavior 再配置 / capability への統合

| 現在 | 目標 | 備考 |
|---|---|---|
| `internal/feature/research/` | **`behavior/research/`** | task.go / tool_*.go / search.go / fetch.go に役割分離 |
| `internal/feature/video/` | **`behavior/video/`** | tool_watch.go / tool_look.go 形、ほぼそのまま |
| `internal/feature/action/` | **`behavior/action/`** + `domain/action/` | ScheduledAction を domain へ |
| `internal/feature/diary/` | **`capability/memory/task_summarize.go`** に統合 | 会話要約 maintenance task、domain/diary 不要 |
| `internal/feature/forget/` | **`capability/memory/task_forget.go`** に統合 | 記憶忘却 maintenance task |
| `internal/feature/topics/` | **`capability/conversation/task_boredom.go`** に統合 | 暇度計算 maintenance task |

### 7.4 port / domain 分解（capability 未使用の package）

| 現在 | 目標 | 備考 |
|---|---|---|
| `internal/chat/` | **そのまま `port/chat/`** | 既に interface のみの package、移動のみ |
| `internal/tool/tool.go` (interfaces) | `port/tool/` へ | Tool, ReadOnlyTool, ToolResult, Content を port 化 |
| `internal/tool/registry.go` (Registry) | `runtime/toolregistry/` へ | 実装は runtime に残す |
| `internal/user/` | `domain/user/` + `port/user/` + `adapter/store/user/` に 3 分割 | 純データストア、capability 不要（Entity 7 型、Store/AdminStore/BotRegistrar interface、DBStore SQL 実装） |

### 7.5 Session / channel 再配置

| 現在 | 目標 | 備考 |
|---|---|---|
| `agent/{cli,device,discord,web}_session.go` | 各 `channel/<name>/session.go` へ移動 | Session は入出力プロトコル固有 |
| web 入力経路（現状 hub 経由で散在） | `channel/web/` に集約 | source.go / session.go / sender.go を揃える |

### 7.6 interface / 型の廃止

| 現在 | 目標 | 備考 |
|---|---|---|
| `scheduler.Feature` interface | **廃止** | behavior は `task.go` / `tool_*.go` で個別に port/scheduler.Task / port/tool.Tool を満たす。DI で登録 |
| `Feature.Setup(ctx, *sql.DB)` | **廃止** | schema migration は `adapter/store/<name>/migrations/` or 専用 migration tool に分離 |
| `api/admin/store.go` の shadow 型 (8 個) | **廃止** | `Action` / `ActionListOpts` / `ActionUpdateFields` / `DiaryEntry` / `Location` / `UserLocation` / `DeviceMapping` / `Place` を `domain/<name>/` に一本化 |
| `api/admin/store.go` の shadow interface (3 個) | **廃止** | `ActionStore` / `DiaryStore` / `LocationStore` は domain 統合により不要 |
| `di/admin_adapter.go` の型変換関数 | **廃止** | `diaryStoreAdapter` / `diaryReaderAdapter` 等の変換は domain 統合で不要 |
| `memento/acquirer.Completer` `memento/consolidator.Completer` | **重複解消** | memory 統合により 1 つに |

### 7.7 連鎖的に解消される問題

| 現象 | 解消手段 |
|---|---|
| `agent/memory.Store` 直接 import（10 箇所） | memory 統合と `port/memory.Memory` 導入で runtime / behavior / tool が port 経由に置換（最大の refactor 範囲） |
| `tool/builtin/memo.go` の `memory.AdminStore` 依存 | memo 専用の狭い interface（consumer-side）に分離 |
| scheduler の広範な依存（channel / llm / memory / tool / user） | `scheduler.Feature` 廃止で連鎖解消、scheduler は `port/scheduler.Task` のみ知る |
| conversation の `llm.Message` 依存 | `domain/message/` 昇格で解消、conversation → domain/message のみ |

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

2 軸で判定：**軸 A = 「契約か実装か」**、**軸 B = 「誰から呼ばれるか」**。

### Step 1：契約（interface 定義のみ）か？

- 複数 package で共有する interface → **`port/<name>/`**
- 1 package 内だけで使う interface → その package 内（consumer-side、Go 慣習）

### Step 2：値型・データだけか？

- 複数 package から参照される Entity / Value Object → **`domain/<name>/`**
- 1 package 内に閉じる中間構造 → その package 内

### Step 3：実装はどこから呼ばれるか？

```
  ├─ 外部サービス / DB / 永続化の具象実装
  │     → adapter/<kind>/<vendor>/   例：adapter/llm/openai/, adapter/store/memory/
  │
  ├─ 外部から HTTP で push されるもの（webhook）
  │     → adapter/<name>/<provider>/
  │
  ├─ 会話 I/O プロトコル（Discord, CLI, Web, Device）
  │     → channel/<protocol>/
  │
  ├─ HTTP 管理 API
  │     → データ CRUD：api/admin/handler_<resource>.go
  │     → ランタイム操作：api/control/handler_<area>.go
  │
  ├─ agent loop 自体（pipeline / session / scheduler 等）
  │     → runtime/<name>/
  │
  ├─ agent が「持つ」能力（他から呼ばれる、port あり）
  │     → capability/<name>/ + port/<name>/
  │
  ├─ agent が「する」行動（scheduler / LLM に駆動される、port なし）
  │     → behavior/<name>/
  │
  ├─ Task 実装 → behavior/<name>/task.go
  │
  ├─ Tool 実装 → §6.4 参照
  │       ・プロトコル固有 → channel/<name>/tool_*.go
  │       ・capability の機能公開 → capability/<name>/tool_*.go
  │       ・独立行動 → behavior/<name>/tool_*.go
  │       ・汎用 → behavior/builtin/tool_*.go
  │
  ├─ stdlib 補完の純関数
  │     → lib/<name>/
  │
  └─ 副作用ありの cross-cutting（log, metrics, tracing）
        → observe/
```

### lib/ vs observe/ の区別

| 基準 | `lib/` | `observe/` |
|---|---|---|
| 純関数か | ✓（副作用なし） | ✕（ログ送信・metric 発行あり） |
| 例 | `lib/jtime/`、`lib/crypto/` | metrics / tracing / log ring buffer |

---

## 10. 強制手段

- `depguard` lint rule で import 違反を静的検知
- CI で `go vet` + depguard を走らせる
- 違反はビルドエラー

### 10.1 `.depguard.yml` の書き方サンプル

sibling 禁止を許可リスト展開で書くと爆発するので、**deny + allow 組み合わせ** で書く：

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

**ポイント**：

- `files` でルール適用ディレクトリを絞る
- `deny` を先に書いて禁止対象を宣言、必要なら例外的に `allow` で許す形（capability 自身の package は Go 的に同 package なら OK）
- 全内部パッケージの列挙は避ける（メンテ不能）

---

## 11. スコープ外

- 現状からの **移行手順**（別ドキュメント）
- `runtime/pipeline/` 内部のファイル分割規則
- リント設定の具体：`.golangci.yml` / `.depguard.yml` で管理
