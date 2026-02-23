---
applyTo: "**"
paths: "internal/memory/**,internal/user/**,internal/consolidator/**"
---

# データ層

## メモリストア

`internal/memory/`

- `Memory` 型: ID, Type (`user`/`world`/`tool`), Content, Embedding(`[]float32`), Metadata(`map[string]any`), CreatedAt, UpdatedAt
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
