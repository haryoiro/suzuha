# アーキテクチャ移行計画

`target-layout.md` で定義した目標形への段階的な移行手順。各フェーズは **独立してビルド・テストが通る** ことを条件とし、順序を守れば各段階で main に merge できる。

---

## 0. 前提

- 移行中、既存の機能は壊さない（feature flag 等は使わず、純粋な package 配置変更として行う）
- 各フェーズ終了時に `docker compose exec agent go build ./agent/...` + `go test ./agent/...` が通る
- Phase 単位でコミット／PR を分ける

---

## Phase 1：ゼロリスク整備（観察のみ、import 変更なし）

### 目的
後続の破壊的変更の前準備。既存コードを動かしたまま足場を作る。

### 作業
1. **`docs/architecture/target-layout.md` レビュー完了**（済）
2. **depguard の baseline 設定** — 現状の import 関係を snapshot し、新規違反のみ検知する形で導入
3. **`.claude/rules/architecture.md` を target-layout.md の要約に差し替え** — LLM とコーディング時に設計が参照される
4. 現行の import graph を可視化（参考資料）

### 完了条件
- target-layout.md がレビューされ、追加決定事項が反映済み
- depguard lint が CI で動作、新規違反を検知可能

---

## Phase 2：domain/ の新設と型昇格

### 目的
重複している型を単一定義に統合。最低コストで最大の整理効果。

### 作業
1. `agent/internal/domain/` ディレクトリ作成
2. 以下の型を移動（現行から切り出し）：
   - `domain/memo/` ← memory package の Memo / MemoryType / Keywords
   - `domain/user/` ← user package の User / PlatformLink / UserGuild / MentionableUser / GuildSummary / ChannelEntry / GuildChannel
   - `domain/message/` ← llm package の Message / Role
   - `domain/channel/` ← 現行の ChannelID / PlatformID / Source kind（散在している場合は集約）
   - `domain/diary/` ← feature/diary/store.go の Entry / Period / EntryKind
   - `domain/action/` ← feature/action/store.go の Action / ActionListOpts / ActionUpdateFields / ActionStatus
   - `domain/location/` ← feature/location/store.go の Location / UserLocation / DeviceMapping / Place
3. `api/admin/store.go` の shadow 型 8 つを削除し、domain 型を import
4. `di/admin_adapter.go` の変換関数を削除
5. `scheduler/task.go` 等で使われている memory.Store 依存の一部を domain/memo 型に置き換え

### 完了条件
- 型定義の重複ゼロ（`Action` / `Location` 等の同名型が複数 package に無い）
- `api/admin/store.go` が interface 定義のみになる（型は domain/ 参照）
- ビルド・テスト通過

### 影響範囲（目安）
- 変更ファイル：30〜50
- 破壊的変更なし（名前空間の移動のみ）

---

## Phase 3：port/ の新設（既存 interface を移動）

### 目的
consumer-side interface を `port/` に集約し、Hexagonal の契約層を可視化。

### 作業
1. `agent/internal/port/` ディレクトリ作成
2. 既に interface-only の package をそのまま移動：
   - `internal/chat/` → `port/chat/`
   - `internal/tool/tool.go` の interface 群 → `port/tool/`
   - `internal/user/user.go` の Store / AdminStore / BotRegistrar → `port/user/`
3. scheduler.Task interface を定義して `port/scheduler/` に配置（既存 CronTask は内部型として残す）
4. 全 import 文を更新

### 完了条件
- 既存機能のロジックは一切変更しない
- import path の置換だけで完了
- ビルド・テスト通過

### 影響範囲（目安）
- 変更ファイル：80〜100（import 置換メイン）
- 機械的な置換が大半

---

## Phase 4：driver/ の新設と external/ 廃止

### 目的
外部 SDK 実装を `driver/` に集約、`external/` を消す。

### 作業
1. `agent/internal/driver/` ディレクトリ作成
2. 外部 SDK wrapper を移動：
   - `external/transcript/` → `driver/transcript/`
   - `external/embedding/` → `driver/embedder/<vendor>/`
   - `external/tts/` → `driver/tts/<vendor>/`
   - `external/stt/` → `driver/stt/<vendor>/`
   - `external/twitter/` → `driver/twitter/`
3. `port/` に薄い port を定義：
   - `port/embedder/`
   - `port/tts/`
   - `port/stt/`
   - `port/vad/`
   - `port/transcript/`
4. driver が port を実装する形に改める
5. `agent/external/` 削除

### 完了条件
- `agent/external/` が存在しない
- 各 driver が port interface を満たす

---

## Phase 5：user capability 廃止（domain + port + driver に分解）

### 目的
user package はロジックが無いので capability を作らず純粋な domain/port/driver 分解。

### 作業
1. `internal/user/store.go`（DBStore 実装）→ `driver/store/user/`
2. `internal/user/provider.go` の DI 登録を新構成に合わせて更新
3. `internal/user/` ディレクトリ削除

### 完了条件
- user 関連で capability/ 配下にディレクトリが無い

---

## Phase 6：memory capability 統合（memento + memory）

### 目的
最大の refactor。memory と memento を 1 つの capability に集約し、port/memory を切る。

### 作業
1. `agent/internal/capability/memory/` ディレクトリ作成
2. ファイル統合：
   - `memento/acquirer/*.go` → `capability/memory/acquire*.go`
   - `memento/consolidator/*.go` → `capability/memory/consolidate*.go`
   - `memory/store.go` の Store interface → consumer-side で分割
   - `memory/postgres*.go` → `driver/store/memory/`
3. `port/memory/` に interface 定義：
   - `port/memory.Memory` — agent から使う主 API
   - `port/memory.Admin` — admin から使う管理 API
   - `port/memory.Media` — media attachment 用
   - Backend は port 化せず driver/ 内部
4. 現行 `memory.Store` 直接 import 10 箇所を `port/memory` 経由に置換
5. `memento/acquirer.Completer` / `memento/consolidator.Completer` 重複 interface を統合
6. `memo/builtin/memo.go` の AdminStore 依存を狭い専用 interface に分離

### 完了条件
- `internal/memento/` 削除
- `internal/memory/` は空（または Backend の薄い型のみ）
- port/memory.Memory が agent の唯一の memory 接点

### 影響範囲（目安）
- 変更ファイル：50〜80
- 最大の refactor フェーズ。**独立した PR で扱う**

---

## Phase 7：llm capability の port 抽出

### 目的
concrete `*llm.Client` を interface 化、port/llm を新設。

### 作業
1. 現行 `*llm.Client` の公開メソッドを洗い出し
2. `port/llm.Client` interface を consumer 側から設計
3. `port/llm.Admin`（ProviderRegistry 管理）を分離
4. `capability/llm/` に現行 llm package 中身を移動
5. `adapter/llm/<vendor>/` に vendor 別の実装を切り出す
6. 全 consumer の import を port 経由に置換

### 完了条件
- llm.Client を直接受ける signature が cmd/di 以外に無い
- 新 vendor 追加が adapter/llm/<new>/ の 1 ディレクトリ追加で済む

---

## Phase 8：feature/ を behavior/ + capability/ に分解

### 目的
feature/ 階層を廃止し、意味論的に capability と behavior に再配置。

### 作業
1. `agent/internal/behavior/` 作成、以下を移動：
   - `feature/diary/` → `behavior/diary/`
   - `feature/research/` → `behavior/research/`
   - `feature/wander/` → `behavior/wander/`
   - `feature/forget/` → `behavior/forget/`
   - `feature/topics/` → `behavior/topics/`
   - `feature/video/` → `behavior/video/`
   - `feature/action/` → `behavior/action/`
2. capability に昇格：
   - `feature/vision/` → `capability/vision/` + `port/vision/`
   - `feature/location/` → `capability/location/` + `port/location/`
   - `internal/voice/` → `capability/voice/` + `port/{stt,tts,vad}/`
3. 各 package 内部を `<name>.go` / `<verb>.go` / `store.go` / `<storage>.go` / `task.go` / `tool_<verb>.go` のファイル命名規則に整理
4. `feature/` ディレクトリ削除

### 完了条件
- `agent/internal/feature/` が存在しない
- capability / behavior のファイル内で 1 ファイル = 1 責務が守られている

---

## Phase 9：scheduler.Feature 廃止

### 目的
Feature interface による bundle を解体し、各 behavior が直接 Task / Tool を export する形に。

### 作業
1. `scheduler.Feature` interface を削除
2. 各 behavior の `task.go` が直接 `port/scheduler.Task` を満たすよう修正
3. `tool_*.go` が直接 `port/tool.Tool` を満たすよう修正
4. `Feature.Setup(ctx, *sql.DB)` を廃止、schema migration は `driver/store/<name>/migrations/` に移行
5. DI で個別 Task / Tool を直接登録する形に変更
6. `scheduler/feature.go` 削除

### 完了条件
- `scheduler.Feature` interface が存在しない
- DI 側で全 behavior の Task / Tool を個別登録できている

---

## Phase 10：Session 実装を channel/ に移動

### 目的
`internal/agent/*_session.go` を各 channel に戻す（プロトコル固有）。

### 作業
1. `agent/cli_session.go` → `channel/cli/session.go`
2. `agent/device_session.go` → `channel/device/session.go`
3. `agent/discord_session.go` → `channel/discord/session.go`
4. `agent/web_session.go` → `channel/web/session.go`（`channel/web/` 新設）
5. `agent/session.go` の共通部分は `core/session/`（or `runtime/session/`）に残す
6. channel-specific tools（voice_join 等）を `channel/<name>/tool_*.go` に整理

### 完了条件
- `agent/*_session.go` が存在しない
- 各 channel が session + source + sender + channel-specific tool を自己完結で持つ

---

## Phase 11：channel 再配置 と adapter 廃止

### 目的
`internal/adapter/` を `internal/channel/` にリネーム、全プロトコル揃える。

### 作業
1. `internal/adapter/cli/` → `internal/channel/cli/`
2. `internal/adapter/device/` → `internal/channel/device/`
3. `internal/adapter/discord/` → `internal/channel/discord/`
4. `channel/web/` 新設（Phase 10 で Session は入れ済み、source / sender を追加）
5. `internal/adapter/` 削除

### 完了条件
- `internal/adapter/` が存在しない
- channel/ 配下 4 プロトコル全て同じ構造（source.go / session.go / sender.go）

---

## Phase 12：observe / lint の整備

### 目的
framework 対象外の util を整理、depguard を厳格モードへ。

### 作業
1. `internal/observe/` を framework 外 util としての扱いに整理（コメント・doc 更新）
2. depguard を baseline モードから厳格モードに切り替え
3. CI で全 import 違反をビルドエラー化
4. `.claude/rules/architecture.md` を最終版に更新

### 完了条件
- depguard がすべての違反を検知
- CI が green

---

## フェーズ依存グラフ

```
Phase 1 (準備)
  ↓
Phase 2 (domain/)
  ↓
Phase 3 (port/) ←┬── Phase 4 (driver/, external 廃止)
  ↓              │
Phase 5 (user)   │
  ↓              │
Phase 6 (memory) ─┘
  ↓
Phase 7 (llm)
  ↓
Phase 8 (feature → behavior/capability)
  ↓
Phase 9 (Feature interface 廃止)
  ↓
Phase 10 (Session 移動)
  ↓
Phase 11 (channel 再配置)
  ↓
Phase 12 (lint 厳格化)
```

並列実行可能な組み合わせ：

- Phase 3 と Phase 4（両方とも既存コードの移動）
- Phase 5 と Phase 7（user と llm は独立）

---

## 各フェーズの規模感

| Phase | 変更ファイル数（目安） | 難度 | PR 粒度 |
|---|---|---|---|
| 1 | 5〜10 | 🟢 | 1 PR |
| 2 | 30〜50 | 🟢 | 1 PR |
| 3 | 80〜100 | 🟡 | 1 PR |
| 4 | 40〜60 | 🟡 | 1 PR |
| 5 | 20〜30 | 🟢 | 1 PR |
| 6 | 50〜80 | 🔴 | **独立 PR** |
| 7 | 40〜60 | 🔴 | **独立 PR** |
| 8 | 30〜50 | 🟡 | 1〜2 PR |
| 9 | 15〜25 | 🟡 | 1 PR |
| 10 | 15〜20 | 🟢 | 1 PR |
| 11 | 10〜15 | 🟢 | 1 PR |
| 12 | 5〜10 | 🟢 | 1 PR |

合計：約 13〜14 PR、全体で数百ファイル変更。

---

## スコープ外

- 新機能の追加は本計画では扱わない
- パフォーマンス改善は別途
- spec（TypeSpec）側の再編は別途
