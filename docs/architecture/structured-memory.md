# メモリアーキテクチャ — 構造化メモリと consolidator ライブラリ

## 1. 概要

consolidator を「メモリのライフサイクルを所有する単一ライブラリ」とし、Memory struct に構造化フィールド（Keywords, Topic, Persons, EventTime）を追加。3軸検索（FTS + Vec + Symbolic）で検索品質を向上させた。

## 2. 実装完了フェーズ

### Phase 1: 構造化メモリ + consolidator ライブラリ化 ✓

- Memory struct に Keywords, Topic, Persons, EventTime 追加（migration 00028）
- consolidator を 10 ファイルに分割（抽出 + 保守を統一）
- forget/ を薄い scheduler adapter に
- JSON 抽出フォーマット + Force Disambiguation ルール
- 既存メモリコンテキスト（ListRecent）で重複防止

### Phase 2: テスタビリティ改善 ✓

- maintain.go → maintain.go + cluster.go + judge.go に分割
- AdminStore に ListEmbeddedMemories/ListAllEmbeddings 追加 → admin.DB() 依存排除
- completer interface 導入 → LLM mock 可能
- テスト 42 ケース追加

### Phase 3: Symbolic 検索 + 3軸 RRF ✓

- SymbolicFilter 型 + SearchWithContext を Store に追加
- searchSymbolic: persons の json_each, topic LIKE, event_time フィルタ
- rrfMerge3: 3軸 Reciprocal Rank Fusion
- Agent の buildMemoryContext が会話参加者 ID を Symbolic フィルタに渡す

## 3. メモリ読み取りパスの全体像と現状の断絶

### 全読み取りパス一覧

```
Agent Think (エフェメラルコンテキスト構築):
├─ buildMemoryContext    → SearchWithContext(3軸)   ← Persons カラム使用 ✅
├─ buildUserProfilesWith → ListByUser()             ← metadata.user_id 使用 ⚠️
│                        → ListEpisodesByParticipant ← metadata.participants 使用 ⚠️
├─ episodeSignal         → ListEpisodesByParticipant ← metadata.participants 使用 ⚠️
├─ buildDiaryContext     → ListRecentByType(self)   ← 人物無関係、問題なし
└─ buildOtherChannels    → 検索なし（Context 内データ）

Agent Perceive:
└─ injectChannelHistory  → SearchRecent(2軸)        ← Symbolic なし、ただしレアケース

Diary:
├─ hourly                → ListRecentByType(各型)   ← 人物無関係、問題なし
└─ daily                 → ListRecentByType(self)   ← 人物無関係、問題なし

Topics:
└─ task                  → SearchRecent             ← 人物無関係、問題なし

Tool (memo):
└─ memo_search           → SearchByType(memo)       ← 人物無関係、問題なし

Consolidator:
├─ extract               → ListRecent               ← 重複防止用、問題なし
└─ server                → IsDuplicateBatch          ← 重複検出、問題なし

Admin:
├─ handler_memory        → List, Get                ← 管理UI、問題なし
└─ handler_media         → Get, SearchByParts       ← 管理UI、問題なし
```

### 断絶: 新旧データパスの混在

**書き込み側**（consolidator/parse.go）:
- `Persons` カラムに正規データを書く ✅
- `metadata.user_id` / `metadata.participants` にも併記（後方互換） ⚠️

**読み取り側の状況**:

| メソッド | 使うフィールド | 対象者 | 問題 |
|---|---|---|---|
| `SearchWithContext` | `persons` カラム (json_each) | 全参加者 | なし ✅ |
| `ListByUser` | `metadata.user_id` (json_extract) | 特定ユーザー | 旧パス ⚠️ |
| `ListEpisodesByParticipant` | `metadata.participants` (json_each) | 特定ユーザー | 旧パス ⚠️ |

**結果**: `buildMemoryContext` だけが新 Persons カラムの恩恵を受け、ユーザープロファイル構築とエピソードシグナルは旧 metadata パスのまま。

### 移行のブロッカー

Phase 1 以前に作成された既存メモリは `persons` カラムが NULL。`ListByUser` / `ListEpisodesByParticipant` を `persons` カラムに切り替えると、旧メモリが見えなくなる。

**解決策**: 既存メモリの `persons` カラムをバックフィルするデータマイグレーション。

```sql
-- user 型: metadata.user_id → persons
UPDATE memories SET persons = json_array(json_extract(metadata, '$.user_id'))
WHERE type = 'user' AND persons IS NULL AND json_extract(metadata, '$.user_id') IS NOT NULL;

-- episode 型: metadata.participants → persons
UPDATE memories SET persons = json_extract(metadata, '$.participants')
WHERE type = 'episode' AND persons IS NULL AND json_extract(metadata, '$.participants') IS NOT NULL;
```

バックフィル完了後:
1. `ListByUser` を `WHERE persons LIKE '%"userID"%'` または `json_each(persons)` に変更
2. `ListEpisodesByParticipant` も同様
3. parse.go の metadata 併記を廃止

## 4. パッケージ責任境界

```
memory/store.go    — データ型 + 検索 interface の定義
memory/sqlite.go   — 検索の SQL 実装（格納形式の詳細はここに閉じる）
consolidator/      — メモリの生成方法・保守方法（抽出、統合、ルール）
agent/think.go     — 何を検索するか・結果をどう使うか
```

### 変更が閉じるパッケージの対応表

| 変更内容 | 影響パッケージ | agent 変更 |
|---|---|---|
| 抽出プロンプト・ルール変更 | consolidator のみ | 不要 |
| JSON → 新フォーマット | consolidator のみ | 不要 |
| merge ロジック変更 | consolidator のみ | 不要 |
| 検索 SQL の WHERE 変更 | memory/sqlite.go のみ | 不要 |
| 新フィールド追加 (struct + migration) | memory + consolidator | 不要 |
| 新検索軸追加 | memory/sqlite.go のみ | 不要（SearchWithContext 内で吸収） |
| ListByUser を persons カラムに切替 | memory/sqlite.go のみ | 不要（interface 不変） |
| metadata 併記の廃止 | consolidator/parse.go のみ | 不要 |

**要点**: agent は `Store interface` を通じてメモリにアクセスしており、格納形式（metadata vs persons カラム）を知らない。格納形式の変更は `memory/sqlite.go`（読み取り）と `consolidator/parse.go`（書き込み）に閉じる。

### Phase 4: 既存データ移行 + 読み取りパス統一 ✓ (2026-03-31)

- `memory/migrations/00029_backfill_persons.sql` — metadata → persons カラムへのバックフィル
- `memory/sqlite.go` — ListByUser / ListEpisodesByParticipant を persons カラム（json_each）に切替
- `consolidator/parse.go` — metadata への user_id/participants 併記を廃止
- agent の変更: なし（interface 不変のため、設計通り）

## 5. 既知の技術的負債

### レガシー JSON フォールバック（consolidator/extract.go）

LLM が JSON を返さない場合、構造化フィールドが全欠落。Error ログで可視化済み。

- **廃止条件**: 本番で1ヶ月間フォールバック発動ゼロ
- **変更箇所**: consolidator/extract.go のみ

### providers.Message の直接依存（consolidator/extract.go, judge.go）

LLM SDK の型が consolidator にリーク。

- **解消**: llm パッケージに wrapper を設ける（llm 側の変更が前提）

### Phase 5: 消費者最適化 ✓ (2026-03-31)

- `agent/think.go` — メモリ表示を metadata → 構造化フィールドに切替
  - `metadata.user_id`/`metadata.participants` 参照を `Memory.Persons` に
  - `EventTime` を優先表示（なければ CreatedAt）
  - `Topic` をラベルに追加

## 6. 残タスク（将来）

- Admin UI に Topic / Persons フィルタ追加（フロントエンド変更）
- consolidator: providers.Message リークの解消（llm 側の変更が前提）

## 7. パッケージ構成

```
consolidator/
  consolidator.go   -- Client / Maintainer interface
  config.go         -- ExtractionConfig, ExtractionRule interface
  server.go         -- Server struct, completer interface, Compact
  extract.go        -- 抽出パイプライン
  parse.go          -- JSON パーサー + レガシーフォールバック
  prompt.go         -- プロンプト組み立て
  rules.go          -- Disambiguation ルール
  maintain.go       -- Maintain パイプライン (~130行)
  cluster.go        -- cosineDistance, buildSimilarityGroups
  judge.go          -- LLM判定 (judgeBatch, parseDecisions)
  merge.go          -- メタデータ + 構造化フィールド統合
  unionfind.go      -- Union-Find
  provider.go       -- DI登録

forget/             -- scheduler adapter のみ
  feature.go / task.go

memory/
  store.go          -- Store / AdminStore interface, Memory struct, SymbolicFilter
  sqlite.go         -- SQLite実装 (searchSymbolic, rrfMerge3, SearchWithContext 等)
```

## 9. 変更が広がらない原則

1. **Memory struct は追加のみ** — 既存フィールドを消さない
2. **DB カラムは NULL 許容追加** — 既存行は影響なし
3. **Store interface は追加のみ** — 既存メソッドの署名を変えない
4. **格納形式の変更は memory + consolidator に閉じる** — agent は interface 経由なので影響しない
5. **forget/ はロジックを持たない** — consolidator に委譲するだけ
6. **consolidator.Client interface は変更しない**
