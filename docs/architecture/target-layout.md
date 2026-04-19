# アーキテクチャ設計書：パッケージ配置の目標形

このドキュメントは、現状の `agent/internal/` の構造的な問題を整理し、**抽象度と依存方向に一貫性のあるパッケージ配置** の目標形を定義する。移行はこの設計を基準に段階的に行う（本書では移行計画には踏み込まず、ゴールの定義に専念する）。

---

## 1. 現状の問題

現行構造の 3 つの構造的欠陥：

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

---

## 2. 設計原則

Clean Architecture の層構造は借りないが、**依存方向の一方向性** という中核アイデアだけを採用する。

### 原則 1：抽象度で段を切る

package は必ず以下の 6 段のどれかに属する：

| 段 | 役割 | 変更頻度 |
|---|---|---|
| `lib/` | stdlib に無い汎用プリミティブ | 稀 |
| `domain/` | 純データ（エンティティ・値型） | 稀 |
| `port/` | interface 定義のみ（契約） | 稀 |
| `core/` | pipeline / agent / session 等の調整役 | 中 |
| `feature/` `tool/` `channel/` `admin/` | 拡張点（プラグイン） | 高 |
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
cmd/                             全てに依存（DI 配線専用）
  ↓
feature/ channel/ tool/ admin/   拡張点
  ↓
core/                            オーケストレーション
  ↓
port/                            契約
  ↓                              ↑
driver/  ─────────────────────── port を満たす実装（core を知らない）
  ↓
domain/                          純データ
  ↓
lib/                             プリミティブ
  ↓
stdlib
```

**同じ段の sibling 間 import は禁止**。共有したくなったら下の段に降ろす。

### 原則 4：`external/` は廃止

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
    │   ├── store/            # Store, Tx, Querier
    │   ├── embedder/         # Embedder
    │   ├── tts/              # Synthesizer
    │   ├── stt/              # Transcriber
    │   ├── vad/              # VoiceActivityDetector
    │   ├── chat/             # chat.Interface, chat.Sender
    │   ├── mcp/              # MCPClient, MCPServer
    │   └── transcript/       # VideoTranscriptFetcher
    │
    ├── core/                 # L3: オーケストレーション
    │   ├── agent/            # Agent 本体、ライフサイクル
    │   ├── pipeline/         # Perceive/Think/Act/Reflect
    │   ├── session/          # per-source 実行コンテキスト
    │   ├── gateway/          # Source 登録 hub (errgroup)
    │   ├── scheduler/        # cron + Feature contract
    │   ├── memory/           # Acquirer + Consolidator + 検索調整
    │   ├── event/            # イベントバス
    │   ├── conversation/     # 会話履歴の保持
    │   ├── tool/             # Tool Registry
    │   └── observe/          # Langfuse, metrics, log ring buffer
    │
    ├── feature/              # L4a: 拡張点（scheduler プラグイン）
    │   ├── diary/
    │   ├── action/
    │   ├── wander/
    │   ├── research/
    │   ├── topics/
    │   ├── forget/
    │   ├── video/
    │   ├── vision/
    │   └── location/
    │
    ├── tool/                 # L4b: 拡張点（LLM から呼ばれる tool）
    │   └── builtin/
    │
    ├── channel/              # L4c: 拡張点（I/O プロトコル）
    │   ├── discord/
    │   ├── device/           # ESP32 WebSocket
    │   ├── web/              # Web widget
    │   └── cli/              # stdin/stdout
    │
    ├── admin/                # L4d: 管理サーフェス
    │   ├── server.go         # HTTP 配信
    │   ├── handler/          # ogen 準拠の handler 実装
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
        ├── store/
        │   ├── sqlite/
        │   └── pg/
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

---

## 4. 依存ルール詳細

### 4.1 許可される import 方向

| from → to | 許可 | 備考 |
|---|---|---|
| `cmd/` → 任意 | ✓ | DI 配線のため |
| `feature/X/` → `core/` | ✓ | scheduler.Feature の実装など |
| `feature/X/` → `port/` | ✓ | LLM や store を使う |
| `channel/X/` → `core/` | ✓ | Source/Session interface の実装 |
| `channel/X/` → `port/` | ✓ | chat.Sender などを使う |
| `admin/` → `core/` | ✓ | agent/scheduler/memory への参照 |
| `core/` → `port/` | ✓ | 契約のみ依存 |
| `core/` → `domain/` | ✓ | 値型の利用 |
| `port/` → `domain/` | ✓ | interface 引数型に domain を使う |
| `driver/X/` → `port/` | ✓ | 実装すべき契約 |
| `driver/X/` → `domain/` | ✓ | 値型の利用 |
| `driver/X/` → `lib/` | ✓ | crypto, jtime 等 |

### 4.2 禁止される import 方向

| from → to | 禁止 | 理由 |
|---|---|---|
| **任意の同段 sibling 間** | ✕ | feature 間・channel 間・driver 間の横断禁止 |
| `driver/` → `core/` | ✕ | driver は port 越しに呼ばれる |
| `driver/` → `feature/` `channel/` `tool/` `admin/` | ✕ | 層逆行 |
| `port/` → `core/` `driver/` | ✕ | 契約は実装を知らない |
| `core/` → `feature/` `tool/` `channel/` `admin/` | ✕ | core はプラグインを知らない |
| `core/` → `driver/` | ✕ | driver は cmd/ で DI 経由で注入される |
| `domain/` → 任意の他 package | ✕（`lib/` 除く） | 純データ |
| `lib/` → 任意の他内部 package | ✕ | プリミティブ |

### 4.3 強制手段

- `depguard` lint rule で静的に検知
- CI で `go vet` + カスタム linter を走らせる
- 違反をコメントでなくビルドエラーに落とす

---

## 5. 配置判定フロー（新しい概念の置き場を決める）

```
新しい package を作りたい
  │
  ├─ 外部サービス/DB を叩くか？
  │     → YES → driver/<kind>/<vendor>/
  │
  ├─ 外界との新しい対話プロトコルか？
  │     → YES → channel/<protocol>/
  │
  ├─ 時間駆動の自律行動か？
  │     → YES → feature/<name>/
  │
  ├─ LLM から呼べる道具か？
  │     → YES → tool/<name>/ or tool/builtin/<name>.go
  │
  ├─ 管理画面経由の操作か？
  │     → YES → admin/<area>/
  │
  ├─ pipeline / session 自体の変更か？
  │     → YES → core/<subsystem>/（慎重に）
  │
  ├─ 複数 consumer が共有する interface か？
  │     → YES → port/<contract>/
  │
  ├─ 純粋なエンティティ/値型か？
  │     → YES → domain/<entity>/
  │
  └─ 汎用的なプリミティブか？
        → YES → lib/<name>/
```

「どれにも当てはまらない」が頻発するなら、その時点で設計書を見直す。

---

## 6. 廃止・統合対象

| 現在 | 目標 | 備考 |
|---|---|---|
| `external/` | 廃止 | 内容はすべて `driver/` へ |
| `internal/adapter/` | 廃止 | 内容は `channel/` へ |
| `internal/memento/` | `core/memory/` に統合 | Acquirer / Consolidator を memory 直下へ |
| `internal/lib/` | `lib/` （位置同じ、役割明確化） | L0 として明示 |

---

## 7. 命名ルール

- ディレクトリ名 = 段 / 役割を表す言葉
- package 名 = ディレクトリ名と一致（Go 慣習）
- interface は port 配下に動詞＋er（`Synthesizer`, `Transcriber`, `Embedder`）か、ドメイン名そのまま（`Store`, `Client`）
- driver サブディレクトリ名 = ベンダー・技術名（`openai`, `sqlite`, `voicevox`）
- `utils/`, `helpers/`, `common/`, `misc/` は禁止（置き場の決まらない package は設計ミス）

---

## 8. このドキュメントのスコープ外

- 現状から目標形への **移行手順** は別ドキュメント（`migration-plan.md`）で扱う
- 各 package 内部のファイル配置規則は個別に定める（例：handler / store / repo パターン）
- リント設定の具体ルールは `.golangci.yml` と `depguard` 設定で管理
