# 記憶システム

エージェントの長期記憶は SQLite に保存され、ベクトル埋め込みによるセマンティック検索で取得される。

## 記憶の種類

| type | 用途 |
|------|------|
| `user` | ユーザーに関する事実（好み、職業、特徴など） |
| `world` | 世界の情報（イベント、ニュース、一般的な事実） |
| `tool` | ツール実行結果の記録 |
| `episode` | 会話のエピソード（要約） |
| `self` | 自分自身に関する情報 |

## ストア構造

**パッケージ:** `internal/memory/`

```go
// internal/memory/store.go
type Store interface {
    Save(ctx, *Memory) error
    Search(ctx, query, limit) ([]Memory, error)
    SearchByType(ctx, query, memType, limit) ([]Memory, error)
    SearchRecent(ctx, query, limit, since) ([]Memory, error)
    SearchByParts(ctx, parts, limit) ([]Memory, error)
    ListByUser(ctx, userID, limit) ([]Memory, error)
    ListByType(ctx, memType, limit) ([]Memory, error)
    ListEpisodesByParticipant(ctx, userID, limit) ([]Memory, error)
    ListRecentByType(ctx, memType, since, limit) ([]Memory, error)
    IsDuplicate(ctx, content, memType) (dupID, emb, error)
    IsDuplicateBatch(ctx, candidates) ([]DupResult, error)
    Close() error
}
```

`Get`, `Delete` 等は `AdminStore` インターフェースに定義されている。

**実装:** `SQLiteStore`（`internal/memory/sqlite.go`）

## ベクトル検索

### 埋め込みワーカー

`store.RunEmbeddingWorker(ctx)` がバックグラウンドで動作し、埋め込みが未生成の記憶に対してベクトルを計算する。

**フロー:**
1. `memories_vec` に存在しない記憶を検出（バッチサイズ 20）
2. `embedder.EmbedBatch()` でベクトルを一括生成（マルチモーダル対応: テキスト + 画像添付）
3. ベクトルを `memories_vec` に保存
4. エラー時はエクスポネンシャルバックオフ（最大 10 分）

### ハイブリッド検索

1. **FTS5**: trigram トークナイザーによるキーワード検索
2. **KNN**: sqlite-vec によるベクトル類似度検索
3. **RRF マージ**: 両方の結果を Reciprocal Rank Fusion (K=60) で統合
4. マルチモーダルブースト: 画像/音声添付のある記憶は距離を 1.5x/1.4x で割り引き

## DB スキーマ

**主要テーブル:**

```sql
-- 記憶本体
CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT,  -- JSON
    created_at DATETIME,
    updated_at DATETIME
);

-- ベクトル埋め込み (sqlite-vec 仮想テーブル)
-- memories_vec は sqlite-vec の KNN 検索用

-- 全文検索 (FTS5 trigram)
-- memories_fts は FTS5 仮想テーブル

-- ユーザー情報
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    display_name TEXT,
    role TEXT DEFAULT '',
    is_bot BOOLEAN DEFAULT FALSE,
    closeness REAL DEFAULT 0,
    trust REAL DEFAULT 0,
    interest REAL DEFAULT 0,
    metadata TEXT,
    created_at DATETIME,
    updated_at DATETIME
);

-- プラットフォームリンク
CREATE TABLE user_platform_links (
    user_id TEXT REFERENCES users(id),
    platform TEXT,
    platform_user_id TEXT,
    platform_name TEXT,
    PRIMARY KEY (platform, platform_user_id)
);

-- 好感度イベント
CREATE TABLE affinity_events (
    id TEXT PRIMARY KEY,
    user_id TEXT REFERENCES users(id),
    axis TEXT,      -- 'closeness', 'trust', 'interest'
    delta REAL,
    reason TEXT,
    created_at DATETIME
);

-- チャンネル設定
CREATE TABLE channel_settings (
    channel_id TEXT PRIMARY KEY,
    guild_id TEXT,
    mode TEXT DEFAULT 'active',  -- 'active', 'listen', 'disabled'
    home BOOLEAN DEFAULT FALSE,
    updated_at DATETIME
);

-- チャンネルアクティビティ
CREATE TABLE channel_activity (
    channel_id TEXT PRIMARY KEY,
    last_user_message_at DATETIME
);

-- コンテキストスナップショット
CREATE TABLE context_snapshot (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    messages TEXT,  -- JSON
    updated_at DATETIME
);

-- 会話ログ（ファインチューニングデータ用）
CREATE TABLE conversation_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id TEXT,
    channel_id TEXT,
    role TEXT,
    content TEXT,
    user_id TEXT,
    user_name TEXT,
    message_id TEXT,
    tool_calls TEXT,  -- JSON
    tool_call_id TEXT,
    timestamp DATETIME
);

-- アプリケーション設定
CREATE TABLE app_settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- タスク状態永続化
CREATE TABLE task_state (
    task_name TEXT PRIMARY KEY,
    state TEXT  -- JSON
);
```

## コンテキスト永続化

エージェントの会話コンテキスト（LLM に送信するメッセージ履歴）は `context_snapshot` テーブルに保存される。

- **保存タイミング:** Reflect ステージ完了時、コンテキスト圧縮後
- **復元タイミング:** エージェント起動時 (`loadContext()`)
- **形式:** メッセージ配列の JSON

これにより、エージェントの再起動後も会話の文脈が維持される。

## 記憶の利用箇所

| フェーズ | 利用方法 |
|---------|---------|
| Think | セマンティック検索で関連記憶をエフェメラルとして注入 |
| Think | ユーザーに関する記憶をプロフィールに含める |
| Think | 自己認識記憶を注入 |
| Topics | 最近の記憶・独り言を参照して重複回避 |
| Explore | 探索結果を `remember: true` で保存 |
| Consolidator | 圧縮時にエピソード記憶を生成 |
| Forget | 類似記憶の検出・マージ |
