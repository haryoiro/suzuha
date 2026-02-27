---
applyTo: "**"
paths: "internal/memory/**,internal/user/**,internal/consolidator/**"
---

# データ層

## メモリストア

`internal/memory/`

- `Memory` 型: ID, Type (`user`/`world`/`tool`/`rss`/`episode`), Content, Embedding(`[]float32`), Metadata(`map[string]any`), CreatedAt, UpdatedAt
- `episode` 型の Metadata: `participants`([]string), `emotional_tone`(string) — Consolidator が会話の出来事を記録
- `EmbedFunc` — embedding 生成関数。`main.go` でクロージャとして注入（循環依存回避）

### Store インターフェース

- `Save(ctx, *Memory)` — embedding があればベクトルストアにも保存、FTS5 にもインデックス
- `Search(ctx, query, limit)` — ハイブリッド検索（FTS5 + sqlite-vec → RRF マージ）
- `SearchByType(ctx, query, memType, limit)` — 型でフィルタした検索
- `SearchRecent(ctx, query, limit, since)` — `since` 以降のメモリのみ検索。FTS は SQL で、vec は Go 側で時間フィルタ（sqlite-vec は日付フィルタ不可）
- `Close()`

### AdminStore（Store を拡張、管理画面用）

- `List(ctx, ListOpts)` — ページネーション + フィルタ
- `Get(ctx, id)`, `Update(ctx, *Memory)`, `Delete(ctx, id)`
- `DB()` — 直接クエリ用の `*sql.DB`

### 実装

- SQLite + FTS5 + sqlite-vec
- マイグレーション: `internal/memory/migrations/` に SQL ファイル（goose で管理）
- ハイブリッド検索: FTS5 キーワードスコア + ベクトルコサイン類似度 → Reciprocal Rank Fusion でマージ

## ユーザーストア

`internal/user/`

- `User` 型: ID, DisplayName, Role (`owner`/`member`/`guest`), Affinity(`float64`), Metadata, PlatformLinks, timestamps
- `PlatformLink`: Platform, PlatformUserID, PlatformName → 内部ユーザーに紐付け
- `AffinityEvent`: UserID, Delta, Reason, MessageIndices, CreatedAt — consolidator が抽出

### Store インターフェース

- `Resolve(ctx, platform, platformUserID, platformName)` — プラットフォーム横断でユーザー特定、存在しなければ自動作成
- `Get(ctx, id)` — 内部IDで取得
- `UpdateDisplayName(ctx, userID, displayName)` — 表示名更新
- `UpdateAffinity(ctx, *AffinityEvent)` — 親和度デルタ適用 + イベント記録
- `GetAffinity(ctx, userID, limit)` — 直近の親和度イベント取得

## コンソリデータ

`internal/consolidator/`

### Client インターフェース

- `Compact(ctx, *CompactRequest) (*CompactResult, error)`
- `CompactRequest`: Messages(`[]Message`) + TargetCount(`int`)
- `CompactResult`: KeepIndices(`[]int`) + Memories(`[]Memory`) + AffinityDeltas(`[]AffinityDelta`)

### サーバー処理フロー

1. 全メッセージを受け取り、LLM（`CompleteRaw`）に取捨選択を依頼
2. LLM が KEEP（保持インデックス）、MEMORIES（長期記憶）、AFFINITY（親和度変化）を返す
3. 抽出した Memory を DB に保存、AffinityDelta をエージェントに返却
4. エージェントは `KeepOnly(indices)` で短期記憶を圧縮し、`applyAffinityDeltas()` で親和度を更新

### gRPC プロトコル

- 定義: `proto/consolidator/v1/consolidator.proto`
- 生成コード: `gen/consolidator/v1/`
- 通知サービス: `proto/notification/v1/notification.proto` → `gen/notification/v1/`

### フォールバック

consolidator 不通時はエージェント側で `TruncateOldest()` により古いメッセージを切り捨て。

## メトリクステーブル

`internal/observe/` が Agent プロセスから書き込み、`internal/admin/handler/` が Admin プロセスから直接読み取る。
詳細は `observe.md` を参照。

### metrics

| カラム | 型 | 説明 |
|--------|------|------|
| name | TEXT PK | メトリクス名（`suzuha_llm_tokens_input_total` 等） |
| labels | TEXT PK | JSON エンコードされたラベル（`{}` or `{"tool":"fetch","status":"success"}`） |
| value | REAL | 現在の値 |
| updated_at | DATETIME | 最終更新日時 |

Counter/Gauge は `labels = '{}'` の 1 行。CounterVec はラベル組み合わせごとに 1 行。
Histogram の sum/count は `name + "_sum"` / `name + "_count"` として格納。

### metric_histogram_buckets

| カラム | 型 | 説明 |
|--------|------|------|
| name | TEXT PK | ヒストグラム名（`suzuha_llm_latency_seconds` 等） |
| le | REAL PK | バケット上限値 |
| count | INTEGER | 累積カウント |

## チャンネルアクティビティテーブル

`channel_activity` — Topics タスクのバックオフ判定に使用。Agent がユーザーメッセージを受信するたびに更新。

| カラム | 型 | 説明 |
|--------|------|------|
| channel_id | TEXT PK | チャンネル ID |
| last_user_message_at | DATETIME | 最後のユーザーメッセージ受信時刻 |

Topics タスクは `last_user_message_at > 前回投稿時刻` で反応有無を判定する。

## RSS 関連テーブル

RSS Feature が使用。`internal/rss/store.go` で操作。

### rss_feeds

| カラム | 型 | 説明 |
|--------|------|------|
| id | TEXT PK | UUID |
| name | TEXT | フィード表示名 |
| url | TEXT | フィード URL |
| channel_id | TEXT | 通知先チャンネル ID |
| enabled | BOOLEAN | 有効フラグ |
| last_polled_at | DATETIME | 最終取得日時 |

### rss_items

| カラム | 型 | 説明 |
|--------|------|------|
| id | TEXT PK | UUID |
| feed_id | TEXT FK | フィード ID |
| guid | TEXT | 記事の GUID（重複排除用） |
| title | TEXT | 記事タイトル |
| link | TEXT | 記事 URL |
| description | TEXT | 記事概要 |
| published_at | DATETIME | 公開日時 |
| memory_id | TEXT | 長期記憶の ID（ベクトル検索用） |
| notified | BOOLEAN | 通知済みフラグ |
