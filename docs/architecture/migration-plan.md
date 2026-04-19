# アーキテクチャ移行計画

`target-layout.md` で定義した目標形への段階的な移行手順。各フェーズは **独立してビルド・テストが通る** ことを条件とし、順序を守れば各段階で main に merge できる。

---

## 0. 前提

- 移行中、既存の機能は壊さない（feature flag 等は使わず、純粋な package 配置変更として行う）
- 各フェーズ終了時に `docker compose exec agent go build ./agent/...` + `go test ./agent/...` が通る
- Phase 単位でコミット／PR を分ける

## 運用フロー（ralph loop スタイル）

1. **ブランチ戦略**：Phase ごとに `refactor/phase-<N>` ブランチを切って PR 作成
2. **成功判定**：
   - `go build -buildvcs=false ./agent/...` 成功
   - `go test -tags fts5 ./agent/...` 成功
   - `bash scripts/reload.sh` で agent コンテナ再起動成功
   - **Discord (`chat_id=1484450828302680154`) で Phase 関連 tool を試して期待通り応答**
3. **失敗時**：壊れた状態を commit + push、PR 作成後に PR コメントで `@haryoiro` メンション
4. **loop 実装**：Claude Code `/loop` スキル（self-paced）で Phase を順次進める

### reload 手順

Go コード変更後は air による自動 reload が無効なので、必ず明示 reload：

```bash
bash scripts/reload.sh
```

- 所要 ~20 秒（コンテナ内で go build → exec）
- ビルド失敗時はコンテナ exit、`docker compose logs agent` で確認

### Phase 別 Discord 検証（参考）

各 Phase で改変した部分に関係する tool を実際に discord で試す。chat_id: `1484450828302680154`。

| Phase | 検証メッセージ例 | 期待される挙動 |
|---|---|---|
| 0 | 「徘徊して」「今どこにいる？」 | wander/location が無いので LLM が「分からない」等。エラーにならない |
| 1 | 「こんにちは」 | 通常応答（前準備フェーズ、挙動変わらず） |
| 2 | 「メモ保存して：テスト」 | memo tool 動作（domain 昇格後も動く） |
| 3 | 「おはよう」 + tool 呼び出し | port 移動後も全 tool が動く |
| 4 | 「画像生成して」「音声で返事」 | TTS/STT/embedder 実装の adapter 経由で動く |
| 5 | 「俺のこと覚えてる？」 | user store 分解後も user resolve 動作 |
| 6 | 「何か覚えたことある？」「忘れて：X」 | memory search / memo tool / summary task |
| 7 | 任意の対話 | LLM call（port/llm 経由）が動く |
| 8a | Discord voice チャンネルで話しかける | voice capability 経由で VAD/STT/TTS |
| 8b | 「今何見えてる？」（device 接続時） | vision tool 動作 |
| 8d | 「ググって：golang generics」 | research tool 動作 |
| 9 | 任意の対話 + cron task 待ち | scheduler.Feature 廃止後も task 登録 OK |
| 10-12 | 通常対話 | session 移動後も会話成立 |

### 失敗時の PR コメントテンプレ

```
@haryoiro
Phase <N> で失敗しました。

**現象**: <Discord 応答内容 / ビルド失敗 / テスト失敗>
**再現手順**: このブランチで `bash scripts/reload.sh` → Discord に「<テスト message>」送信
**ログ**:
```
<logs>
```
```

---

## Phase 0：事前削除（不要機能の除去）

### 目的
移行対象を減らすため、筋悪と判断した機能を先に削除。

### 作業
1. `internal/feature/wander/` 削除（524 行、Task + Tool）
2. `internal/feature/location/` 削除（1200+ 行）
3. `api/admin/handler_location.go` 削除
4. `spec/admin/routes/location.tsp` 削除、spec 再生成で `api/admin/gen/` の location 関連を除去
5. `api/control/runtime_handler.go` の location 依存部削除
6. DI provider.go の wander / location 登録を削除
7. `config.yaml` の location セクション削除

### 完了条件
- grep で wander / location がコードベースから消えている
- `go build ./agent/...` が通る
- 各 admin UI の location 画面が消えている

### 影響範囲
- 変更ファイル：20〜30
- 純粋な削除なので他 Phase への依存なし
- **最初に実施する PR**

---

## Phase 1：ゼロリスク整備（観察のみ、import 変更なし）

### 目的
後続の破壊的変更の前準備。既存コードを動かしたまま足場を作る。

### 作業
1. **`docs/architecture/target-layout.md` レビュー完了**（済）
2. **depguard の baseline 設定** — `.depguard.yml` に現状の import 関係を snapshot。**各 Phase 終了時に baseline 更新**（段階的に厳しくなる運用）
3. **`.claude/rules/architecture.md` を target-layout.md の要約に差し替え** — LLM とコーディング時に設計が参照される
4. **import graph の可視化**（任意、ツールは `go-callvis` or `goda`、出力は docs/architecture/import-graph.svg）

### baseline 運用ポリシー

- 各 Phase 終了時に depguard baseline を再生成し、違反が増えていないことを確認
- 新規違反は **その Phase 内で解消** するのがルール（baseline に追加しない）
- Phase 12 で baseline を完全廃止、ルールのみ残す

### 完了条件
- target-layout.md がレビューされ、追加決定事項が反映済み
- depguard lint が CI で動作、新規違反を検知可能
- baseline 再生成スクリプト（`scripts/update-depguard-baseline.sh` 等）が用意されている

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
3. scheduler の Task を port 化：
   - `port/scheduler.Task` interface を新設（現行 `scheduler.CronTask` の public シグネチャから起こす）
   - 既存 `CronTask` は runtime/scheduler/ 内部型として残す（Phase 9 で Feature 廃止と同時に整理）
4. 全 import 文を更新

### 完了条件
- 既存機能のロジックは一切変更しない
- import path の置換だけで完了
- ビルド・テスト通過

### 影響範囲（目安）
- 変更ファイル：80〜100（import 置換メイン）
- 機械的な置換が大半

---

## Phase 4：adapter/ の拡張と external/ 廃止

### 目的
外部 SDK 実装を `adapter/` に集約、`external/` を消す。

### 注意：adapter の「リネーム」ではなく「拡張」
- **旧 `internal/adapter/{cli,device,discord}/`** は Phase 11 で `channel/` にリネーム移動する（別の作業）
- 本 Phase では `adapter/` に **cross-cutting 実装用サブディレクトリを新設**

### 作業
1. `agent/internal/adapter/` 配下にカテゴリ別サブディレクトリを作成：
   - `adapter/llm/`、`adapter/embedder/`、`adapter/tts/`、`adapter/stt/`、`adapter/vad/`、`adapter/transcript/`、`adapter/twitter/`
   - `adapter/store/` は Phase 5-8 で capability 移行と同時に中身を入れる（本 Phase では空箱）
2. 外部 SDK wrapper を移動：
   - `external/transcript/` → `adapter/transcript/`
   - `external/embedding/` → `adapter/embedder/<vendor>/`
   - `external/tts/` → `adapter/tts/<vendor>/`
   - `external/stt/` → `adapter/stt/<vendor>/`
   - `external/twitter/` → `adapter/twitter/`
3. `port/` に薄い port を定義：
   - `port/embedder/`、`port/tts/`、`port/stt/`、`port/vad/`、`port/transcript/`
4. 各 adapter/ が port を実装する形に改める
5. `agent/external/` 削除

### 完了条件
- `agent/external/` が存在しない
- 各 adapter/ が port interface を満たす
- 旧 `internal/adapter/{cli,device,discord}/` はそのまま（Phase 11 で移動）

---

## Phase 5：user capability 廃止（domain + port + driver に分解）

### 目的
user package はロジックが無いので capability を作らず純粋な domain/port/driver 分解。

### 作業
1. `internal/user/store.go`（DBStore 実装）→ `adapter/store/user/`
2. `internal/user/provider.go` の DI 登録を新構成に合わせて更新
3. `internal/user/` ディレクトリ削除

### 完了条件
- user 関連で capability/ 配下にディレクトリが無い

---

## Phase 6：memory capability 統合（memento + memory）

### 目的
最大の refactor。memory と memento を 1 つの capability に集約し、port/memory を切る。

### 前提
- Phase 2（domain/）、Phase 3（port/）、Phase 4（adapter/）が完了していること

### 作業
1. `agent/internal/capability/memory/` ディレクトリ作成
2. ファイル統合：
   - `memento/acquirer/*.go` → `capability/memory/acquire*.go`
   - `memento/consolidator/*.go` → `capability/memory/consolidate*.go`
   - `memory/store.go` の Store interface → consumer-side で分割
   - `memory/postgres*.go` → `adapter/store/memory/`
3. `port/memory/` に interface 定義：
   - `port/memory.Memory` — agent から使う主 API
   - `port/memory.Management` — admin から使う管理 API（api/admin/ との名前衝突回避のため Admin ではなく Management）
   - `port/memory.Media` — media attachment 用
   - Backend は port 化せず adapter/ 内部
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

## Phase 8a：voice capability 化

### 目的
`internal/voice/` を capability として整理、STT/TTS/VAD を個別 port に分解。

### 作業
1. `internal/voice/` → `capability/voice/` に移動
2. `port/stt/`, `port/tts/`, `port/vad/` に interface 定義（既に Phase 4 で空箱として作成済み想定）
3. 既存 `external/tts/`, `external/stt/` 実装を `adapter/{tts,stt}/<vendor>/` として port を満たす形に
4. `channel/discord/`, `channel/device/` の voice 依存を capability/voice 経由に書き換え

### 完了条件
- `internal/voice/` 削除
- voice pipeline が 3 port（stt / tts / vad）を DI で受け取る形
- discord / device が capability/voice 経由で voice pipeline を使う

### 影響範囲（目安）
- 変更ファイル：40〜60
- **独立 PR 推奨**

---

## Phase 8b：vision capability 化

### 目的
`internal/feature/vision/` を capability として整理、camera pipeline と tool を分離。

### 作業
1. `internal/feature/vision/` → `capability/vision/` に移動
2. `port/vision/` に interface 定義（FrameProcessor, ChangeDetector 等）
3. YOLO 等の外部実装は `adapter/vision/yolo/` に分離
4. device channel との連携を port 経由に置き換え
5. vision 内の Tool は **`capability/vision/tool_*.go` として capability 内に残す**（target-layout §6.4 の「capability の機能を薄くラップして LLM に公開」に該当）

### 完了条件
- `internal/feature/vision/` 削除
- port/vision.FrameProcessor が capability/vision の主契約

### 影響範囲（目安）
- 変更ファイル：30〜50
- **独立 PR 推奨**

---

## Phase 8d：残り feature を behavior/ に移動

### 目的
capability 化されなかった feature を `behavior/` に移動、ファイル内部の役割分離も同時に実施。

### 作業
1. `agent/internal/behavior/` 作成、以下を移動＆整理：
   - `feature/research/` → `behavior/research/`（search.go / fetch.go / summarize.go / tool_search.go / tool_fetch.go / task.go 等）
   - `feature/video/` → `behavior/video/`（transcript.go / look.go / tool_watch.go / tool_look.go）
   - `feature/action/` → `behavior/action/`（action.go / store.go / postgres.go / task.go / tool_*.go）
2. **maintenance task を capability/ に統合**（Phase 6 と連動）：
   - `feature/diary/daily.go hourly.go` → `capability/memory/task_summarize.go`
   - `feature/forget/task.go` → `capability/memory/task_forget.go`
   - `feature/topics/task.go` → `capability/conversation/task_boredom.go`
2. 各 package 内部を `<name>.go` / `<verb>.go` / `store.go` / `<storage>.go` / `task.go` / `tool_<verb>.go` のファイル命名規則に整理
3. `feature/` ディレクトリ削除

### 完了条件
- `agent/internal/feature/` が存在しない
- 各 behavior のファイル内で 1 ファイル = 1 責務が守られている

### 影響範囲（目安）
- 変更ファイル：50〜80
- 複数 PR に分割可（behavior 単位で独立して作業できる）

---

## Phase 9：scheduler.Feature interface 削除

### 目的
Feature interface による bundle を解体し、各 behavior が直接 Task / Tool を export する形に。**schema migration の移行は既に Phase 6-8 で完了済み**（各 capability / behavior の Phase 内で同時に実施）。

### 前提
- Phase 6〜8d 完了（各 capability / behavior の schema migration は既に `adapter/store/<name>/migrations/` へ移行済み）
- したがって Feature.Setup(ctx, db) の呼び出しはもはや何もしない（空実装）

### 作業
1. `scheduler.Feature` interface を削除
2. 各 behavior / capability の `task.go` が直接 `port/scheduler.Task` を満たすよう修正
3. `tool_*.go` が直接 `port/tool.Tool` を満たすよう修正
4. DI で個別 Task / Tool を直接登録する形に変更（Feature.Tools() / Feature.Tasks() 経由を廃止）
5. `scheduler/feature.go` 削除
6. 空になった Setup メソッドを全 behavior から削除

### 完了条件
- `scheduler.Feature` interface が存在しない
- DI 側で全 behavior の Task / Tool を個別登録できている
- `Setup(ctx, *sql.DB)` メソッドがコードから消えている

### schema migration の移行タイミング（前段 Phase との調整）

| Phase | 同時に実施する schema 移行 |
|---|---|
| Phase 6（memory） | memento/acquirer + consolidator の CREATE TABLE を `adapter/store/memory/migrations/` へ |
| Phase 7（llm） | llm_presets 関連の schema を `adapter/store/llm/migrations/` へ |
| Phase 8a（voice） | voice_sessions 等を `adapter/store/voice/migrations/` へ（もしあれば） |
| Phase 8b（vision） | vision 関連 schema を `adapter/store/vision/migrations/` へ |
| Phase 8c（location） | location/overland 関連を `adapter/store/location/migrations/` へ |
| Phase 8d（behavior 各種） | diary/research/wander/action/forget/topics の schema を各 `adapter/store/<name>/migrations/` へ |

---

## Phase 10：Session 実装を channel/ に移動

### 目的
`internal/agent/*_session.go` を各 channel に戻す（プロトコル固有）。

### 作業
1. `agent/cli_session.go` → `channel/cli/session.go`
2. `agent/device_session.go` → `channel/device/session.go`
3. `agent/discord_session.go` → `channel/discord/session.go`
4. `agent/web_session.go` → `channel/web/session.go`（`channel/web/` 新設）
5. `agent/session.go` の共通部分は `runtime/session/` に残す
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
Phase 0 (事前削除: wander / location)
  ↓
Phase 1 (準備)
  ↓
Phase 2 (domain/) ────────────┐
  ↓                           │
Phase 3 (port/) ──────┬───────┼─── Phase 4 (adapter/, external 廃止)
  ↓                   │       │       ↓
Phase 5 (user) ───────┤       │       │
                      ↓       ↓       ↓
                  Phase 6 (memory capability 統合)  ◀ 全部に依存
                      ↓
                  Phase 7 (llm capability)
                      ↓
      ┌───────────────┴───────────┐
      ↓                           ↓
  Phase 8a (voice)            Phase 8b (vision)
      └───────────┬───────────────┘
                  ↓
              Phase 8d (残り behavior)
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

- **Phase 3 ⊥ Phase 4**（両方とも既存コードの移動、相互独立）
- **Phase 5 ⊥ Phase 7**（user と llm は独立）
- **Phase 8a / 8b**（voice / vision は相互独立、2 並列可能）

---

## 各フェーズの規模感

| Phase | 内容 | 変更ファイル数（目安） | 難度 | PR 粒度 |
|---|---|---|---|---|
| 0 | wander / location 削除 | 20〜30 | 🟢 | 1 PR |
| 1 | 準備 | 5〜10 | 🟢 | 1 PR |
| 2 | domain/ 新設 | 30〜50 | 🟢 | 1 PR |
| 3 | port/ 新設 | 80〜100 | 🟡 | 1 PR |
| 4 | adapter/ + external 廃止 | 40〜60 | 🟡 | 1 PR |
| 5 | user 分解 | 20〜30 | 🟢 | 1 PR |
| 6 | memory capability 統合 | 50〜80 | 🔴 | **独立 PR** |
| 7 | llm capability | 40〜60 | 🔴 | **独立 PR** |
| 8a | voice capability | 40〜60 | 🟡 | **独立 PR** |
| 8b | vision capability | 30〜50 | 🟡 | **独立 PR** |
| 8d | 残り behavior 移動 | 40〜60 | 🟡 | 1〜数 PR |
| 9 | Feature interface 廃止 | 15〜25 | 🟡 | 1 PR |
| 10 | Session 移動 | 15〜20 | 🟢 | 1 PR |
| 11 | channel 再配置 | 10〜15 | 🟢 | 1 PR |
| 12 | lint 厳格化 | 5〜10 | 🟢 | 1 PR |

合計：約 **15〜17 PR**、全体で数百ファイル変更。

---

## 触らない領域

以下は本計画の対象外（基本的に現状維持）：

| package | 方針 |
|---|---|
| `internal/observe/` | framework 対象外の util として維持。移動のみ（internal 直下に残す）、中身は変更しない |
| `internal/lib/` | そのまま維持（位置は変わらず、役割を明示するだけ） |
| `internal/di/` | composition root として維持。各 Phase で DI 配線の更新のみ |
| `internal/api/admin/gen/`, `internal/api/control/gen/` | ogen 生成物、手を入れない（spec 側変更で自動再生成） |
| `agent/cmd/` 配下のバイナリ | 各 Phase で DI と import の更新のみ、ロジックは触らない |

## web hub コード源の扱い

現状 `agent/web_session.go` 経由で web 入力が扱われているが、hub（WebSocket サーバ）の本体コードの所在は移行時に要特定：

- 候補 1：現行 `cmd/suzuha-agent/main.go` 内に hub 生成コードが埋まっている可能性
- 候補 2：`internal/voice/` か `internal/adapter/device/` 経由

Phase 10 / Phase 11 の着手時に hub コードを特定し、`channel/web/` の source として取り込む。

## スコープ外

- 新機能の追加は本計画では扱わない
- パフォーマンス改善は別途
- spec（TypeSpec）側の再編は別途
- `runtime/pipeline/` 内部のファイル分割細則（必要になったら別ドキュメント）

## クロスリファレンス

本計画の各 Phase は `target-layout.md` の対応箇所を参照：

| Phase | target-layout.md の参照先 |
|---|---|
| 0 | §7.0 事前削除 |
| 2 | §3 目標ディレクトリ `domain/`、§7.6 shadow 型廃止 |
| 3 | §3 `port/`、§6 port パターン A〜D |
| 4 | §3 `adapter/`、§2 原則 6（adapter 配置） |
| 5 | §7.4 user 3 分割 |
| 6 | §4.2 capability/memory 標準形、§6.5 port 分割指針 |
| 7 | §7.2 llm capability 昇格 |
| 8a / 8b | §7.2 capability 昇格の voice / vision |
| 8d | §4.1 behavior 標準形、§7.3 behavior 再配置 |
| 9 | §2 原則 6 の schema migration 例外、§7.6 Feature 廃止 |
| 10 / 11 | §7.5 Session / channel 再配置 |
| 12 | §10 強制手段 |
